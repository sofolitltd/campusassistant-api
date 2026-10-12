package handler

import (
	"errors"
	"net/http"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Shared plumbing for the student/teacher/staff "claim a profile with a
// verification code" endpoints. The claiming user always comes from the JWT
// — never from the request body — and the role a user gets comes from the
// profile they claimed, never from what the client asked for at signup.

var errClaimInvalidCode = &reviewError{http.StatusNotFound, "Invalid or already claimed code"}

// claimCaller returns the signed-in user. A legacy client may still send
// user_id in the body; it must match the token.
func claimCaller(c *gin.Context, bodyUserID *uuid.UUID) (uuid.UUID, bool) {
	raw, ok := c.Get("user_id")
	userID, _ := raw.(uuid.UUID)
	if !ok || userID == uuid.Nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Please sign in to claim a profile"})
		return uuid.Nil, false
	}
	if bodyUserID != nil && *bodyUserID != uuid.Nil && *bodyUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only claim a profile for your own account"})
		return uuid.Nil, false
	}
	return userID, true
}

// claimProfile atomically links the unclaimed profile holding code to userID.
// profile must point at an empty Student/Teacher/Staff; it is loaded by code,
// updated with updates (plus the claim fields), and reloaded. alreadyLinked
// counts rows of the same type already linked to userID.
func claimProfile(
	c *gin.Context,
	db *gorm.DB,
	profile interface{},
	code string,
	userID uuid.UUID,
	role domain.Role,
	updates map[string]interface{},
	alreadyLinked func(tx *gorm.DB) int64,
	placement func() (universityID, departmentID uuid.UUID),
) bool {
	err := db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("verification_code = ? AND is_claimed = ?", code, false).
			First(profile).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errClaimInvalidCode
			}
			return err
		}
		if alreadyLinked(tx) > 0 {
			return &reviewError{http.StatusConflict, "Your account is already linked to a profile"}
		}

		now := time.Now()
		updates["user_id"] = userID
		updates["is_claimed"] = true
		updates["claimed_at"] = now
		updates["verification_code"] = ""
		res := tx.Model(profile).Where("is_claimed = ?", false).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errClaimInvalidCode
		}

		uniID, deptID := placement()
		userUpdates := map[string]interface{}{
			"role":        role,
			"is_verified": true,
		}
		if uniID != uuid.Nil {
			userUpdates["university_id"] = uniID
		}
		if deptID != uuid.Nil {
			userUpdates["department_id"] = deptID
		}
		if err := tx.Model(&domain.User{}).Where("id = ?", userID).Updates(userUpdates).Error; err != nil {
			return err
		}
		return tx.First(profile).Error
	})
	if err != nil {
		var re *reviewError
		if errors.As(err, &re) {
			c.JSON(re.status, gin.H{"error": re.message})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to claim profile"})
		}
		return false
	}
	return true
}
