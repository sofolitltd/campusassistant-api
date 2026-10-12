package handler

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type StudentHandler struct {
	*GenericHandler[domain.Student]
	db *gorm.DB
}

func NewStudentHandler(u usecase.Usecase[domain.Student], db *gorm.DB) *StudentHandler {
	return &StudentHandler{
		GenericHandler: NewGenericHandler(u),
		db:             db,
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

// ClaimProfile links the signed-in user to the unclaimed Student holding the
// code. The user comes from the JWT. Only personal details (phone, blood
// group, hall) can be filled in here — roll, batch, session, department and
// university stay as the CR/admin entered them, so a code can't be used to
// move a profile into another class.
func (h *StudentHandler) ClaimProfile(c *gin.Context) {
	var req struct {
		Code       string     `json:"code" binding:"required"`
		UserID     *uuid.UUID `json:"user_id"` // legacy; must match the token if sent
		Phone      string     `json:"phone"`
		BloodGroup string     `json:"blood_group"`
		HallID     *uuid.UUID `json:"hall_id"`
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
	if req.BloodGroup != "" {
		updates["blood_group"] = req.BloodGroup
	}

	if req.HallID != nil {
		var target domain.Student
		var hall domain.Hall
		if err := h.db.Where("verification_code = ? AND is_claimed = ?", req.Code, false).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Invalid or already claimed code"})
			return
		}
		if err := h.db.First(&hall, "id = ?", *req.HallID).Error; err != nil || hall.UniversityID != target.UniversityID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Selected hall does not belong to your university"})
			return
		}
		updates["hall_id"] = *req.HallID
	}

	var student domain.Student
	if !claimProfile(c, h.db, &student, req.Code, userID, domain.RoleStudent, updates,
		func(tx *gorm.DB) int64 {
			var n int64
			tx.Model(&domain.Student{}).Where("user_id = ?", userID).Count(&n)
			return n
		},
		func() (uuid.UUID, uuid.UUID) { return student.UniversityID, student.DepartmentID },
	) {
		return
	}
	c.JSON(http.StatusOK, student)
}

// findByUserID resolves the Student record linked to a JWT user_id — the
// ownership check the plain admin-gated /students/:id route doesn't have.
func (h *StudentHandler) findByUserID(c *gin.Context, userID uuid.UUID) (*domain.Student, error) {
	filter := map[string]interface{}{"user_id": userID}
	students, _, err := h.Usecase.GetAll(c.Request.Context(), filter, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(students) == 0 {
		return nil, errors.New("student not found")
	}
	return &students[0], nil
}

// GetMyStudent returns the current user's own Student record.
// GET /my/student
func (h *StudentHandler) GetMyStudent(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	student, err := h.findByUserID(c, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student profile not found"})
		return
	}
	c.JSON(http.StatusOK, student)
}

// UpdateMyStudent lets the current user edit their own phone/blood
// group/hall — fields EditProfilePage exposes but previously had no real
// backend to save to.
// PUT /my/student
func (h *StudentHandler) UpdateMyStudent(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	student, err := h.findByUserID(c, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student profile not found"})
		return
	}

	var req struct {
		Phone      string     `json:"phone"`
		BloodGroup string     `json:"blood_group"`
		HallID     *uuid.UUID `json:"hall_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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

	if err := h.Usecase.Update(c.Request.Context(), student); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update student profile"})
		return
	}
	c.JSON(http.StatusOK, student)
}

// resolveStudentAddress validates the submitted district/sub-district IDs
// against domain.BDDistricts and returns the denormalized JSONB payload to
// store — never trusting client-submitted names.
func resolveStudentAddress(req *struct {
	DistrictID    string `json:"district_id" binding:"required"`
	SubDistrictID string `json:"sub_district_id"`
	AddressLine   string `json:"address_line"`
}) (*domain.StudentAddressInfo, error) {
	district, ok := domain.FindBDDistrict(req.DistrictID)
	if !ok {
		return nil, errors.New("invalid district_id")
	}

	info := &domain.StudentAddressInfo{
		DistrictID:   district.ID,
		DistrictName: district.Name,
		AddressLine:  req.AddressLine,
	}

	if req.SubDistrictID != "" {
		subDistrict, ok := domain.FindBDSubDistrict(req.DistrictID, req.SubDistrictID)
		if !ok {
			return nil, errors.New("invalid sub_district_id")
		}
		info.SubDistrictID = subDistrict.ID
		info.SubDistrictName = subDistrict.Name
	}

	return info, nil
}

// UpdateMyAddress sets the current user's present/permanent address (either
// may be omitted to leave it unchanged).
// PUT /my/student/address
func (h *StudentHandler) UpdateMyAddress(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	student, err := h.findByUserID(c, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student profile not found"})
		return
	}

	var req struct {
		PresentAddress *struct {
			DistrictID    string `json:"district_id" binding:"required"`
			SubDistrictID string `json:"sub_district_id"`
			AddressLine   string `json:"address_line"`
		} `json:"present_address"`
		PermanentAddress *struct {
			DistrictID    string `json:"district_id" binding:"required"`
			SubDistrictID string `json:"sub_district_id"`
			AddressLine   string `json:"address_line"`
		} `json:"permanent_address"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.PresentAddress != nil {
		info, err := resolveStudentAddress(req.PresentAddress)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		raw, _ := json.Marshal(info)
		jsonValue := datatypes.JSON(raw)
		student.PresentAddress = &jsonValue
	}

	if req.PermanentAddress != nil {
		info, err := resolveStudentAddress(req.PermanentAddress)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		raw, _ := json.Marshal(info)
		jsonValue := datatypes.JSON(raw)
		student.PermanentAddress = &jsonValue
	}

	if err := h.Usecase.Update(c.Request.Context(), student); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update address"})
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
