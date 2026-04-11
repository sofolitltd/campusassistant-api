package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type StudentHandler struct {
	*GenericHandler[domain.Student]
}

func NewStudentHandler(u usecase.Usecase[domain.Student]) *StudentHandler {
	return &StudentHandler{
		GenericHandler: NewGenericHandler(u),
	}
}

func (h *StudentHandler) Create(c *gin.Context) {
	var student domain.Student
	if err := c.ShouldBindJSON(&student); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate a 6-digit numeric verification code if not provided
	if student.VerificationCode == "" {
		code, err := generateNumericCode(6)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
			return
		}
		student.VerificationCode = code
	}

	if err := h.Usecase.Create(c.Request.Context(), &student); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, student)
}

func (h *StudentHandler) VerifyCode(c *gin.Context) {
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

	students, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(students) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid or already claimed code"})
		return
	}

	c.JSON(http.StatusOK, students[0])
}

func (h *StudentHandler) ClaimProfile(c *gin.Context) {
	var req struct {
		Code         string     `json:"code" binding:"required"`
		UserID       uuid.UUID  `json:"user_id" binding:"required"`
		StudentID    string     `json:"student_id"`
		Phone        string     `json:"phone"`
		BloodGroup   string     `json:"blood_group"`
		HallID       *uuid.UUID `json:"hall_id"`
		BatchID      *uuid.UUID `json:"batch_id"`
		SessionID    *uuid.UUID `json:"session_id"`
		DepartmentID *uuid.UUID `json:"department_id"`
		UniversityID *uuid.UUID `json:"university_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filter := map[string]interface{}{
		"verification_code": req.Code,
		"is_claimed":        false,
	}

	students, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(students) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid or already claimed code"})
		return
	}

	student := students[0]
	student.UserID = &req.UserID
	student.IsClaimed = true
	student.VerificationCode = "" // Clear code after claim

	// Update additional fields if provided
	if req.StudentID != "" {
		student.StudentID = req.StudentID
	}
	if req.Phone != "" {
		student.Phone = req.Phone
	}
	if req.BloodGroup != "" {
		student.BloodGroup = req.BloodGroup
	}
	if req.HallID != nil {
		student.HallID = req.HallID
	}
	if req.BatchID != nil {
		student.BatchID = *req.BatchID
	}
	if req.SessionID != nil {
		student.SessionID = *req.SessionID
	}
	if req.DepartmentID != nil {
		student.DepartmentID = *req.DepartmentID
	}
	if req.UniversityID != nil {
		student.UniversityID = *req.UniversityID
	}

	if err := h.Usecase.Update(c.Request.Context(), &student); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update student profile: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, student)
}

func generateNumericCode(length int) (string, error) {
	result := ""
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		result += fmt.Sprintf("%d", num)
	}
	return result, nil
}
