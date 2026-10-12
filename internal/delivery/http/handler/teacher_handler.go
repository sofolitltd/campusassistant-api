package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type TeacherHandler struct {
	*GenericHandler[domain.Teacher]
	db *gorm.DB
}

func NewTeacherHandler(u usecase.Usecase[domain.Teacher], db *gorm.DB) *TeacherHandler {
	return &TeacherHandler{
		GenericHandler: NewGenericHandler(u),
		db:             db,
	}
}

func (h *TeacherHandler) Create(c *gin.Context) {
	var teacher domain.Teacher
	if err := c.ShouldBindJSON(&teacher); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if teacher.VerificationCode == "" {
		code, err := generateNumericCode(6)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
			return
		}
		teacher.VerificationCode = code
	}

	if err := h.Usecase.Create(c.Request.Context(), &teacher); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, teacher)
}

// VerifyCode checks whether an unclaimed teacher profile exists for the code.
// POST /teachers/verify-code
func (h *TeacherHandler) VerifyCode(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Code is required"})
		return
	}

	filter := map[string]interface{}{
		"verification_code": req.Code,
		"is_claimed":        false,
	}

	teachers, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(teachers) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid or already claimed code"})
		return
	}

	c.JSON(http.StatusOK, teachers[0])
}

// ClaimProfile links an existing User account to an unclaimed Teacher profile.
// POST /teachers/claim-profile
func (h *TeacherHandler) ClaimProfile(c *gin.Context) {
	var req struct {
		Code   string     `json:"code" binding:"required"`
		UserID *uuid.UUID `json:"user_id"` // legacy; must match the token if sent
		Phone  string     `json:"phone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, ok := claimCaller(c, req.UserID)
	if !ok {
		return
	}

	updates := map[string]interface{}{}
	if req.Phone != "" {
		updates["phone"] = req.Phone
	}

	var teacher domain.Teacher
	if !claimProfile(c, h.db, &teacher, req.Code, userID, domain.RoleTeacher, updates,
		func(tx *gorm.DB) int64 {
			var n int64
			tx.Model(&domain.Teacher{}).Where("user_id = ?", userID).Count(&n)
			return n
		},
		func() (uuid.UUID, uuid.UUID) { return teacher.UniversityID, teacher.DepartmentID },
	) {
		return
	}
	c.JSON(http.StatusOK, teacher)
}

// findByUserID resolves the Teacher record linked to a JWT user_id.
func (h *TeacherHandler) findByUserID(c *gin.Context, userID uuid.UUID) (*domain.Teacher, error) {
	filter := map[string]interface{}{"user_id": userID}
	teachers, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(teachers) == 0 {
		return nil, errors.New("teacher not found")
	}
	return &teachers[0], nil
}

// GetMyTeacher returns the current user's own Teacher record.
// GET /my/teacher
func (h *TeacherHandler) GetMyTeacher(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	teacher, err := h.findByUserID(c, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Teacher profile not found"})
		return
	}
	c.JSON(http.StatusOK, teacher)
}
