package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// UserHandler is a small, dedicated self-service handler — distinct from the
// generic /users CRUD (API-key gated, no per-user ownership check) — for the
// JWT user to edit their own identity fields.
type UserHandler struct {
	usecase usecase.Usecase[domain.User]
}

func NewUserHandler(u usecase.Usecase[domain.User]) *UserHandler {
	return &UserHandler{usecase: u}
}

// UpdateMyUser lets the current user edit their own name/photo.
// PUT /my/user
func (h *UserHandler) UpdateMyUser(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	user, err := h.usecase.GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var req struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		AvatarURL string `json:"avatar_url"`
		Gender    string `json:"gender"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.FirstName != "" {
		user.FirstName = req.FirstName
	}
	if req.LastName != "" {
		user.LastName = req.LastName
	}
	if req.AvatarURL != "" {
		user.AvatarURL = req.AvatarURL
	}
	if req.Gender != "" {
		user.Gender = req.Gender
	}

	if err := h.usecase.Update(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user"})
		return
	}
	c.JSON(http.StatusOK, user)
}

// AdminResetPassword lets an admin set a user's password directly.
// PUT /users/:id/password (AdminJWT only)
//
// Exists for support cases where the self-service email code in
// password_reset_handler.go never reaches the user (spam filtering, a dead
// inbox, etc). Bumping TokenVersion mirrors ResetPassword: a reset — admin-
// issued or not — should invalidate whatever sessions were already issued.
func (h *UserHandler) AdminResetPassword(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	user, err := h.usecase.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user.PasswordHash = hash
	user.TokenVersion++
	if err := h.usecase.Update(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password reset successfully"})
}
