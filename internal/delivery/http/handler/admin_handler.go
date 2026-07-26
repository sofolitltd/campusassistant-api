package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/pkg/auth"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AdminHandler struct {
	adminRepo *postgres.AdminRepository
}

func NewAdminHandler(adminRepo *postgres.AdminRepository) *AdminHandler {
	return &AdminHandler{adminRepo: adminRepo}
}

type CreateAdminRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
	Name     string `json:"name" binding:"required"`
	Role     string `json:"role"`
}

func (h *AdminHandler) List(c *gin.Context) {
	admins, err := h.adminRepo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch admins"})
		return
	}
	c.JSON(http.StatusOK, admins)
}

func (h *AdminHandler) Create(c *gin.Context) {
	var req CreateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role := req.Role
	if role == "" {
		role = "admin"
	}

	existing, _ := h.adminRepo.FindByEmail(strings.ToLower(req.Email))
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Admin with this email already exists"})
		return
	}

	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	var createdBy *uuid.UUID
	if userID, exists := c.Get("user_id"); exists {
		if id, ok := userID.(uuid.UUID); ok {
			createdBy = &id
		}
	}

	admin := domain.Admin{
		Email:        strings.ToLower(req.Email),
		PasswordHash: hashedPassword,
		Name:         req.Name,
		Role:         role,
		IsActive:     true,
		CreatedBy:    createdBy,
	}

	if err := h.adminRepo.Create(&admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create admin"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":     admin.ID,
		"email":  admin.Email,
		"name":   admin.Name,
		"role":   admin.Role,
		"active": admin.IsActive,
	})
}

type UpdateAdminRequest struct {
	Name *string `json:"name"`
}

func (h *AdminHandler) Update(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid admin ID"})
		return
	}

	if _, err := h.adminRepo.FindByID(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Admin not found"})
		return
	}

	var req UpdateAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != nil {
		if err := h.adminRepo.UpdateName(id, *req.Name); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update name"})
			return
		}
	}

	admin, _ := h.adminRepo.FindByID(id)
	c.JSON(http.StatusOK, admin)
}

type ChangePasswordRequest struct {
	Password string `json:"password" binding:"required,min=6"`
}

func (h *AdminHandler) ChangePassword(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid admin ID"})
		return
	}

	if _, err := h.adminRepo.FindByID(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Admin not found"})
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := h.adminRepo.UpdatePassword(id, hashedPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password updated successfully"})
}

func (h *AdminHandler) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid admin ID"})
		return
	}

	admin, err := h.adminRepo.FindByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Admin not found"})
		return
	}

	// Prevent deleting self
	currentUserID, _ := c.Get("user_id")
	if currentID, ok := currentUserID.(uuid.UUID); ok && currentID == admin.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete your own account"})
		return
	}

	if err := h.adminRepo.DeleteByID(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete admin"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Admin deleted successfully"})
}
