package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/pkg/auth"
	"campusassistant-api/pkg/mailer"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AuthHandler handles authentication requests
type AuthHandler struct {
	db                *gorm.DB
	jwtManager        *auth.JWTManager
	accessTokenExpiry int // in minutes
	adminRepo         *postgres.AdminRepository
	mailer            mailer.Mailer
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(db *gorm.DB, jwtManager *auth.JWTManager, accessTokenExpiry int, adminRepo *postgres.AdminRepository, mail mailer.Mailer) *AuthHandler {
	return &AuthHandler{
		db:                db,
		jwtManager:        jwtManager,
		accessTokenExpiry: accessTokenExpiry,
		adminRepo:         adminRepo,
		mailer:            mail,
	}
}

// RegisterRequest represents a user registration request
type RegisterRequest struct {
	Email        string     `json:"email" binding:"required,email"`
	Password     string     `json:"password" binding:"required,min=8"`
	FirstName    string     `json:"first_name" binding:"required"`
	LastName     string     `json:"last_name" binding:"required"`
	Phone        string     `json:"phone"`
	Gender       string     `json:"gender"`
	Role         string     `json:"role"` // defaults to 'student' if empty
	UniversityID *uuid.UUID `json:"university_id"`
	DepartmentID *uuid.UUID `json:"department_id"`
}

// LoginRequest represents a login request
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// RefreshTokenRequest represents a refresh token request
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// AuthResponse represents the authentication response
type AuthResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	User         domain.User `json:"user"`
	ExpiresIn    int64       `json:"expires_in"` // seconds until access token expires
}

// Register godoc
// @Summary Register a new user
// @Description Create a new user account with email and password
// @Tags auth
// @Accept json
// @Produce json
// @Param request body RegisterRequest true "Registration details"
// @Success 201 {object} AuthResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if user already exists
	var existingUser domain.User
	if err := h.db.Where("email = ?", req.Email).First(&existingUser).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User with this email already exists"})
		return
	}

	// Hash password
	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set default role if not provided
	role := req.Role
	if role == "" {
		role = string(domain.RoleStudent)
	}

	// Create user
	user := domain.User{
		Email:        strings.ToLower(req.Email),
		PasswordHash: hashedPassword,
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Phone:        req.Phone,
		Gender:       req.Gender,
		Role:         domain.Role(role),
		UniversityID: req.UniversityID,
		DepartmentID: req.DepartmentID,
		IsActive:     true,
		IsVerified:   false,
	}

	if err := h.db.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// Generate tokens
	accessToken, err := h.jwtManager.GenerateAccessToken(
		user.ID,
		user.Email,
		string(user.Role),
		user.UniversityID,
		user.DepartmentID,
		user.TokenVersion,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := h.jwtManager.GenerateRefreshToken(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	c.JSON(http.StatusCreated, AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         user,
		ExpiresIn:    int64(h.accessTokenExpiry * 60), // minutes -> seconds
	})
}

// Login godoc
// @Summary Login user
// @Description Authenticate user with email and password
// @Tags auth
// @Accept json
// @Produce json
// @Param request body LoginRequest true "Login credentials"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Find user by email
	var user domain.User
	if err := h.db.Where("email = ?", strings.ToLower(req.Email)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// Check if user is active
	if !user.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Account is deactivated"})
		return
	}

	// Verify password
	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	// Generate tokens
	accessToken, err := h.jwtManager.GenerateAccessToken(
		user.ID,
		user.Email,
		string(user.Role),
		user.UniversityID,
		user.DepartmentID,
		user.TokenVersion,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := h.jwtManager.GenerateRefreshToken(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	user.ComputeSubscriptionStatus()

	c.JSON(http.StatusOK, AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         user,
		ExpiresIn:    int64(h.accessTokenExpiry * 60),
	})
}

// AdminLogin godoc
// @Summary Admin login
// @Description Authenticate admin with email and password against admins table
// @Tags auth
// @Accept json
// @Produce json
// @Param request body LoginRequest true "Admin credentials"
// @Success 200 {object} AuthResponse
// @Failure 401 {object} map[string]string
// @Router /auth/admin-login [post]
func (h *AuthHandler) AdminLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	admin, err := h.adminRepo.FindByEmail(strings.ToLower(req.Email))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if !admin.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Account is deactivated"})
		return
	}

	if err := auth.VerifyPassword(admin.PasswordHash, req.Password); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	accessToken, err := h.jwtManager.GenerateAccessToken(
		admin.ID,
		admin.Email,
		admin.Role,
		nil,
		nil,
		1,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := h.jwtManager.GenerateRefreshToken(admin.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    int64(h.accessTokenExpiry * 60),
		"admin": gin.H{
			"id":    admin.ID,
			"email": admin.Email,
			"name":  admin.Name,
			"role":  admin.Role,
		},
	})
}

// RefreshToken godoc
// @Summary Refresh access token
// @Description Get a new access token using a refresh token
// @Tags auth
// @Accept json
// @Produce json
// @Param request body RefreshTokenRequest true "Refresh token"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /auth/refresh [post]
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate refresh token
	userID, err := h.jwtManager.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	// Get user from database
	var user domain.User
	if err := h.db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	// Check if user is active
	if !user.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Account is deactivated"})
		return
	}

	// Generate new access token
	accessToken, err := h.jwtManager.GenerateAccessToken(
		user.ID,
		user.Email,
		string(user.Role),
		user.UniversityID,
		user.DepartmentID,
		user.TokenVersion,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"expires_in":   int64(h.accessTokenExpiry * 60),
	})
}

// changeMyPasswordRequest represents a change-password request (authenticated).
type changeMyPasswordRequest struct {
	OldPassword        string `json:"old_password" binding:"required"`
	NewPassword        string `json:"new_password" binding:"required,min=8"`
	LogoutOtherDevices *bool  `json:"logout_other_devices"` // defaults to true
}

// ChangePassword godoc
// @Summary Change password (authenticated user)
// @Description Verify the old password, set a new one, and optionally invalidate all other sessions.
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body changeMyPasswordRequest true "Change password payload"
// @Success 200 {object} AuthResponse
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /auth/change-password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req changeMyPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch user from DB so we have the current password hash + token version
	var user domain.User
	if err := h.db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	if !user.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Account is deactivated"})
		return
	}

	// Verify old password
	if err := auth.VerifyPassword(user.PasswordHash, req.OldPassword); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Old password is incorrect"})
		return
	}

	// Hash new password
	hashedPassword, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	logoutOthers := true
	if req.LogoutOtherDevices != nil {
		logoutOthers = *req.LogoutOtherDevices
	}

	tx := h.db.WithContext(c.Request.Context()).Begin()

	// Update password
	if err := tx.Model(&user).UpdateColumn("password_hash", hashedPassword).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	tokenVersion := user.TokenVersion
	if logoutOthers {
		// Increment token version — invalidates all existing JWTs except the
		// one we're about to issue below
		if err := tx.Model(&domain.User{}).Where("id = ?", user.ID).
			UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to invalidate old sessions"})
			return
		}
		tokenVersion++

		// Wipe all device records for this user so push notifications stop
		// going to other devices
		tx.Where("user_id = ?", user.ID).Delete(&domain.UserDevice{})
	}

	tx.Commit()

	// Re-fetch so the response includes the latest user state
	h.db.First(&user, userID)
	user.ComputeSubscriptionStatus()

	// Issue new access token with the (possibly bumped) token version
	accessToken, err := h.jwtManager.GenerateAccessToken(
		user.ID,
		user.Email,
		string(user.Role),
		user.UniversityID,
		user.DepartmentID,
		tokenVersion,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := h.jwtManager.GenerateRefreshToken(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	c.JSON(http.StatusOK, AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		User:         user,
		ExpiresIn:    int64(h.accessTokenExpiry * 60),
	})
}

// GetMe godoc
// @Summary Get current user
// @Description Get the authenticated user's profile
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} domain.User
// @Failure 401 {object} map[string]string
// @Router /auth/me [get]
func (h *AuthHandler) GetMe(c *gin.Context) {
	// Get user ID from context (set by JWT middleware)
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var user domain.User
	if err := h.db.Preload("Student.Batch").Preload("Student.Department").Preload("Student.University").Preload("Student.Hall").First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Populate batch/hall/blood group from the Student relation — these
	// aren't columns on User itself (see domain.User's Hall/Blood comment).
	if user.Student != nil {
		if user.Student.Batch != nil {
			user.Batch = user.Student.Batch.Name
		}
		if user.Student.Hall != nil {
			user.Hall = user.Student.Hall.Name
		}
		user.Blood = user.Student.BloodGroup
	}
	user.ComputeSubscriptionStatus()

	// Fallback to Student-level university/department if User-level not set
	if user.UniversityID == nil && user.Student != nil {
		user.UniversityID = &user.Student.UniversityID
	}
	if user.DepartmentID == nil && user.Student != nil {
		user.DepartmentID = &user.Student.DepartmentID
	}

	c.JSON(http.StatusOK, user)
}
