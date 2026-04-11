package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/auth"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AuthHandler handles authentication requests
type AuthHandler struct {
	db         *gorm.DB
	jwtManager *auth.JWTManager
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(db *gorm.DB, jwtManager *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		db:         db,
		jwtManager: jwtManager,
	}
}

// RegisterRequest represents a user registration request
type RegisterRequest struct {
	Email        string    `json:"email" binding:"required,email"`
	Password     string    `json:"password" binding:"required,min=8"`
	FirstName    string    `json:"first_name" binding:"required"`
	LastName     string    `json:"last_name" binding:"required"`
	Phone        string    `json:"phone"`
	Gender       string    `json:"gender"`
	Role         string    `json:"role"` // defaults to 'student' if empty
	UniversityID uuid.UUID `json:"university_id"`
	DepartmentID uuid.UUID `json:"department_id"`
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
		ExpiresIn:    int64(15 * time.Minute / time.Second), // 15 minutes in seconds
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
		ExpiresIn:    int64(15 * time.Minute / time.Second),
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
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"expires_in":   int64(15 * time.Minute / time.Second),
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
	if err := h.db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, user)
}
