package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type StaffHandler struct {
	*GenericHandler[domain.Staff]
}

func NewStaffHandler(u usecase.Usecase[domain.Staff]) *StaffHandler {
	return &StaffHandler{
		GenericHandler: NewGenericHandler(u),
	}
}

func (h *StaffHandler) Create(c *gin.Context) {
	var staff domain.Staff
	if err := c.ShouldBindJSON(&staff); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if staff.VerificationCode == "" {
		code, err := generateNumericCode(6)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
			return
		}
		staff.VerificationCode = code
	}

	if err := h.Usecase.Create(c.Request.Context(), &staff); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, staff)
}

// VerifyCode checks whether an unclaimed staff profile exists for the code.
// POST /staffs/verify-code
func (h *StaffHandler) VerifyCode(c *gin.Context) {
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

	staffs, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(staffs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid or already claimed code"})
		return
	}

	c.JSON(http.StatusOK, staffs[0])
}

// ClaimProfile links an existing User account to an unclaimed Staff profile.
// POST /staffs/claim-profile
func (h *StaffHandler) ClaimProfile(c *gin.Context) {
	var req struct {
		Code   string    `json:"code" binding:"required"`
		UserID uuid.UUID `json:"user_id" binding:"required"`
		Mobile string    `json:"mobile"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	filter := map[string]interface{}{
		"verification_code": req.Code,
		"is_claimed":        false,
	}

	staffs, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(staffs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid or already claimed code"})
		return
	}

	staff := staffs[0]
	staff.UserID = &req.UserID
	staff.IsClaimed = true
	staff.VerificationCode = "" // Clear code after claim

	if req.Mobile != "" {
		staff.Mobile = req.Mobile
	}

	if err := h.Usecase.Update(c.Request.Context(), &staff); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update staff profile: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, staff)
}

// findByUserID resolves the Staff record linked to a JWT user_id.
func (h *StaffHandler) findByUserID(c *gin.Context, userID uuid.UUID) (*domain.Staff, error) {
	filter := map[string]interface{}{"user_id": userID}
	staffs, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(staffs) == 0 {
		return nil, errors.New("staff not found")
	}
	return &staffs[0], nil
}

// GetMyStaff returns the current user's own Staff record.
// GET /my/staff
func (h *StaffHandler) GetMyStaff(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	staff, err := h.findByUserID(c, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Staff profile not found"})
		return
	}
	c.JSON(http.StatusOK, staff)
}
