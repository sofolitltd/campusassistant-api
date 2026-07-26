package handler

import (
	"net/http"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"

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
