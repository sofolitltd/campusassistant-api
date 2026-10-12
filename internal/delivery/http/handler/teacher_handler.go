package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TeacherHandler struct {
	*GenericHandler[domain.Teacher]
}

func NewTeacherHandler(u usecase.Usecase[domain.Teacher]) *TeacherHandler {
	return &TeacherHandler{
		GenericHandler: NewGenericHandler(u),
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
		Code   string    `json:"code" binding:"required"`
		UserID uuid.UUID `json:"user_id" binding:"required"`
		Phone  string    `json:"phone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	teacher := teachers[0]
	teacher.UserID = &req.UserID
	teacher.IsClaimed = true
	teacher.VerificationCode = "" // Clear code after claim

	if req.Phone != "" {
		teacher.Phone = req.Phone
	}

	if err := h.Usecase.Update(c.Request.Context(), &teacher); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update teacher profile: " + err.Error()})
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
