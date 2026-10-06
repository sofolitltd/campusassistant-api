package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/auth"
	"campusassistant-api/pkg/logger"
	"campusassistant-api/pkg/mailer"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// The password reset flow is three steps:
//
//	POST /auth/forgot-password    {email, account_type}                        -> generic 200
//	POST /auth/verify-reset-code  {email, account_type, code}                  -> {reset_token}
//	POST /auth/reset-password     {email, account_type, reset_token, password} -> 200
//
// Two properties are load-bearing and easy to break during maintenance:
//
//  1. Step 1 must return an identical response whether or not the account
//     exists, and must not block on SMTP — otherwise response timing alone
//     tells an attacker which emails are registered.
//  2. The token handed out by step 2 is opaque random bytes, NOT a JWT.
//     Minting a JWT here would produce something JWTManager.ValidateToken
//     accepts as a normal login token, turning "I know a 6-digit code" into
//     full account access without ever setting a password.

// genericResetResponse is returned by ForgotPassword in every case — account
// found, account missing, or per-email rate limit hit.
const genericResetResponse = "If an account exists for that email, a reset code has been sent."

type ForgotPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	AccountType string `json:"account_type"` // "user" (default) | "admin"
}

type VerifyResetCodeRequest struct {
	Email       string `json:"email" binding:"required,email"`
	AccountType string `json:"account_type"`
	Code        string `json:"code" binding:"required,len=6,numeric"`
}

type ResetPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	AccountType string `json:"account_type"`
	ResetToken  string `json:"reset_token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// ForgotPassword issues a one-time code and emails it to the account owner.
//
// Always responds 200 with the same body (except for the per-IP throttle,
// which is account-independent and therefore safe to report explicitly).
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	accountType, err := normalizeAccountType(req.AccountType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ip := c.ClientIP()
	since := time.Now().Add(-domain.ResetRateWindow)

	// Per-IP throttle. This one is reported honestly: it does not depend on
	// whether any particular account exists, so it leaks nothing.
	var ipCount int64
	if err := h.db.Model(&domain.PasswordReset{}).
		Where("request_ip = ? AND created_at > ?", ip, since).
		Count(&ipCount).Error; err == nil && ipCount >= domain.MaxResetRequests {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many reset requests. Please wait a few minutes and try again.",
		})
		return
	}

	// Per-email throttle. Deliberately silent: replying "rate limited" here
	// would confirm the address is registered, since rows only exist for real
	// accounts. Over the limit we drop the request and return the same
	// generic message, which also stops the endpoint being used to mail-bomb
	// a known address.
	var emailCount int64
	if err := h.db.Model(&domain.PasswordReset{}).
		Where("email = ? AND account_type = ? AND created_at > ?", email, accountType, since).
		Count(&emailCount).Error; err == nil && emailCount >= domain.MaxResetRequests {
		c.JSON(http.StatusOK, gin.H{"message": genericResetResponse})
		return
	}

	exists, err := h.accountExists(email, accountType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if !exists {
		c.JSON(http.StatusOK, gin.H{"message": genericResetResponse})
		return
	}

	code, err := generateResetCode()
	if err != nil {
		logger.Errorf("Password reset: failed to generate code: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create reset code"})
		return
	}

	codeHash, err := auth.HashCode(code)
	if err != nil {
		logger.Errorf("Password reset: failed to hash code: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create reset code"})
		return
	}

	// Supersede any codes still outstanding for this account, so only the most
	// recently emailed code works.
	now := time.Now()
	if err := h.db.Model(&domain.PasswordReset{}).
		Where("email = ? AND account_type = ? AND consumed_at IS NULL", email, accountType).
		Update("consumed_at", now).Error; err != nil {
		logger.Errorf("Password reset: failed to supersede old codes: %v", err)
	}

	reset := domain.PasswordReset{
		Email:       email,
		AccountType: accountType,
		CodeHash:    codeHash,
		RequestIP:   ip,
		ExpiresAt:   now.Add(domain.ResetCodeTTL),
	}
	if err := h.db.Create(&reset).Error; err != nil {
		logger.Errorf("Password reset: failed to persist reset row: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create reset code"})
		return
	}

	// Send off the request path. SMTP round-trips take hundreds of
	// milliseconds; doing this inline would make "account exists" measurably
	// slower than "account missing" and reintroduce enumeration by timing.
	go func(to, code string) {
		htmlBody, textBody := mailer.PasswordResetBodies(code, int(domain.ResetCodeTTL.Minutes()))
		if err := h.mailer.Send(to, mailer.PasswordResetSubject, htmlBody, textBody); err != nil {
			logger.Errorf("Password reset: failed to email %s: %v", to, err)
		}
	}(email, code)

	c.JSON(http.StatusOK, gin.H{"message": genericResetResponse})
}

// VerifyResetCode exchanges a valid 6-digit code for a single-use reset token.
func (h *AuthHandler) VerifyResetCode(c *gin.Context) {
	var req VerifyResetCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A valid 6-digit code is required"})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	accountType, err := normalizeAccountType(req.AccountType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	var reset domain.PasswordReset
	err = h.db.Where("email = ? AND account_type = ? AND consumed_at IS NULL AND expires_at > ?",
		email, accountType, now).
		Order("created_at DESC").
		First(&reset).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid or expired code"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if reset.Attempts >= domain.MaxResetAttempts {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many incorrect attempts. Please request a new code.",
		})
		return
	}

	if err := auth.VerifyCode(reset.CodeHash, req.Code); err != nil {
		// Count the miss. Once MaxResetAttempts is reached the row is dead
		// even if the correct code arrives afterwards.
		h.db.Model(&reset).Update("attempts", reset.Attempts+1)

		remaining := domain.MaxResetAttempts - (reset.Attempts + 1)
		if remaining <= 0 {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many incorrect attempts. Please request a new code.",
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error":              "Invalid or expired code",
			"attempts_remaining": remaining,
		})
		return
	}

	token, err := generateResetToken()
	if err != nil {
		logger.Errorf("Password reset: failed to generate reset token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify code"})
		return
	}

	// SHA-256 rather than bcrypt: the token is 32 random bytes, so there is no
	// brute-force surface for a fast hash to expose.
	if err := h.db.Model(&reset).Updates(map[string]interface{}{
		"token_hash":  hashResetToken(token),
		"verified_at": now,
		"expires_at":  now.Add(domain.ResetTokenTTL),
	}).Error; err != nil {
		logger.Errorf("Password reset: failed to store reset token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify code"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"reset_token": token,
		"expires_in":  int64(domain.ResetTokenTTL.Seconds()),
	})
}

// ResetPassword consumes a verified reset token and sets the new password.
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Email, reset token and a password of at least 8 characters are required",
		})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	accountType, err := normalizeAccountType(req.AccountType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now()
	var reset domain.PasswordReset
	err = h.db.Where(`email = ? AND account_type = ? AND token_hash = ?
		AND consumed_at IS NULL AND verified_at IS NOT NULL AND expires_at > ?`,
		email, accountType, hashResetToken(req.ResetToken), now).
		First(&reset).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid or expired reset token"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	passwordHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update the credential and burn the token together — a partial failure
	// here would either leave the token reusable or change the password
	// without recording it.
	err = h.db.Transaction(func(tx *gorm.DB) error {
		switch accountType {
		case domain.ResetAccountAdmin:
			// NOTE: domain.Admin has no TokenVersion, and JWTMiddleware pins
			// admin token_version to 1, so an admin's existing sessions
			// survive a password reset. Fixing that needs a schema change on
			// the admins table (see SESSION_MANAGEMENT.md).
			res := tx.Model(&domain.Admin{}).
				Where("email = ?", email).
				Update("password_hash", passwordHash)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		default:
			// Bumping TokenVersion invalidates every access token already
			// issued for this user — the right outcome after a reset, since
			// the reset may well be happening because the account was stolen.
			res := tx.Model(&domain.User{}).
				Where("email = ?", email).
				Updates(map[string]interface{}{
					"password_hash": passwordHash,
					"token_version": gorm.Expr("token_version + 1"),
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}

		return tx.Model(&reset).Update("consumed_at", now).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Account no longer exists"})
			return
		}
		logger.Errorf("Password reset: failed to update password for %s: %v", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Password reset successfully. Please sign in with your new password.",
	})
}

// accountExists reports whether the address maps to an active account, without
// revealing that to the caller.
func (h *AuthHandler) accountExists(email, accountType string) (bool, error) {
	var count int64
	var err error

	if accountType == domain.ResetAccountAdmin {
		err = h.db.Model(&domain.Admin{}).
			Where("email = ? AND is_active = ?", email, true).
			Count(&count).Error
	} else {
		err = h.db.Model(&domain.User{}).
			Where("email = ? AND is_active = ?", email, true).
			Count(&count).Error
	}

	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func normalizeAccountType(accountType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(accountType)) {
	case "", domain.ResetAccountUser:
		return domain.ResetAccountUser, nil
	case domain.ResetAccountAdmin:
		return domain.ResetAccountAdmin, nil
	default:
		return "", fmt.Errorf("account_type must be %q or %q", domain.ResetAccountUser, domain.ResetAccountAdmin)
	}
}

// generateResetCode returns a zero-padded 6-digit code.
//
// crypto/rand, not math/rand: a predictable code is the same as no code at all.
func generateResetCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// generateResetToken returns 32 random bytes hex-encoded (64 characters).
func generateResetToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
