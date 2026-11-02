package handler

import (
	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/usecase"
	"campusassistant-api/pkg/mailer"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StudentHandler struct {
	*GenericHandler[domain.Student]
	db   *gorm.DB
	mail mailer.Mailer
}

func NewStudentHandler(u usecase.Usecase[domain.Student], db *gorm.DB, mail mailer.Mailer) *StudentHandler {
	h := &StudentHandler{
		GenericHandler: NewGenericHandler(u),
		db:             db,
		mail:           mail,
	}
	h.GenericHandler.DB = db
	h.BeforeRespond = func(c *gin.Context, student *domain.Student) {
		if !callerIsAdmin(c) {
			student.VerificationCode = ""
		}
	}
	h.AfterUpdate = func(c *gin.Context, before domain.Student, after *domain.Student) {
		syncLinkedUserName(c, db, after.UserID, before.Name, after.Name)
	}
	return h
}

func (h *StudentHandler) Create(c *gin.Context) {
	var student domain.Student
	if err := c.ShouldBindJSON(&student); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if ok, err := domain.BatchHasSession(h.db.WithContext(c.Request.Context()), student.BatchID, student.SessionID); err != nil || !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Selected session does not belong to the selected batch"})
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

// SendCodeEmail emails a student their profile-claim code so the admin
// dashboard doesn't need to hand codes over by CSV/copy-paste.
// POST /students/:id/send-code-email (AdminJWT).
//
// Synchronous on purpose — the admin is waiting on the result and wants a
// definitive answer, unlike the password-reset flow which fires and forgets.
// With no SMTP_* configured the noop mailer just logs, mirroring how the
// reset flow degrades in development.
func (h *StudentHandler) SendCodeEmail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student id"})
		return
	}

	var student domain.Student
	if err := h.db.First(&student, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student not found"})
		return
	}
	if student.IsClaimed {
		c.JSON(http.StatusConflict, gin.H{"error": "Profile already claimed — nothing to send"})
		return
	}
	if student.VerificationCode == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "Student has no verification code"})
		return
	}

	email := strings.TrimSpace(student.Email)
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Student has no email address on file"})
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Student's email address is not valid"})
		return
	}

	htmlBody, textBody := mailer.ProfileClaimBodies(student.Name, student.VerificationCode)

	var senderID *uuid.UUID
	if raw, ok := c.Get("user_id"); ok {
		if u, ok := raw.(uuid.UUID); ok {
			senderID = &u
		}
	}

	if err := h.mail.Send(email, mailer.ProfileClaimSubject, htmlBody, textBody); err != nil {
		recordEmailLog(h.db, email, mailer.ProfileClaimSubject, domain.EmailKindProfileClaim,
			domain.EmailStatusFailed, err.Error(), senderID, &student.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send email: " + err.Error()})
		return
	}
	recordEmailLog(h.db, email, mailer.ProfileClaimSubject, domain.EmailKindProfileClaim,
		domain.EmailStatusSent, "", senderID, &student.ID)

	c.JSON(http.StatusOK, gin.H{"sent": true, "email": email})
}

// GenerateCode replaces an unclaimed student's verification code with a
// fresh 6-digit numeric one — the repair path for legacy rows that predate
// auto-generation (they can't be claimed or code-mailed without one).
// Claimed profiles are refused: their code may remain for unclaim/verify
// flows and regenerating it there would silently invalidate live sessions
// mid-claim. Uniqueness is best-effort (the column is only indexed) — a
// rare collision just retries with a new draw.
// POST /students/:id/generate-code (AdminJWT).
func (h *StudentHandler) GenerateCode(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid student id"})
		return
	}

	var student domain.Student
	if err := h.db.First(&student, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Student not found"})
		return
	}
	if student.IsClaimed {
		c.JSON(http.StatusConflict, gin.H{"error": "Profile already claimed — code cannot be regenerated"})
		return
	}

	var code string
	for attempt := 0; attempt < 5; attempt++ {
		code, err = generateNumericCode(6)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate code"})
			return
		}
		var n int64
		h.db.Model(&domain.Student{}).
			Where("verification_code = ? AND is_claimed = ? AND id <> ?", code, false, id).
			Count(&n)
		if n == 0 {
			break
		}
	}

	student.VerificationCode = code
	if err := h.db.Save(&student).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save verification code"})
		return
	}

	audit.RecordRequest(c, h.db, audit.Event{
		ViaRole: moderatorAuditRole(c, h.db, student.UniversityID, student.DepartmentID, student.BatchID),
		Action:  "code.regenerate", EntityType: "student", EntityID: student.ID,
		UniversityID: &student.UniversityID, DepartmentID: &student.DepartmentID, BatchID: &student.BatchID,
		Changes: map[string]any{"code": "regenerated"},
	})
	c.JSON(http.StatusOK, student)
}

// RotateUnclaimedCodes invalidates every current profile-claim code without
// returning the replacement codes. The operation is deliberately explicit
// and atomic because it invalidates all previously shared codes at once.
func (h *StudentHandler) RotateUnclaimedCodes(c *gin.Context) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Explicit confirmation is required"})
		return
	}

	var rotated int64
	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var students []domain.Student
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_claimed = false AND user_id IS NULL").
			Order("id ASC").
			Find(&students).Error; err != nil {
			return err
		}

		usedCodes := make(map[string]struct{}, len(students)*2)
		for _, student := range students {
			if student.VerificationCode != "" {
				usedCodes[student.VerificationCode] = struct{}{}
			}
		}
		for _, student := range students {
			code, err := generateUnusedNumericCode(usedCodes)
			if err != nil {
				return err
			}
			result := tx.Model(&domain.Student{}).
				Where("id = ? AND is_claimed = false AND user_id IS NULL", student.ID).
				Update("verification_code", code)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("student %s was not rotated", student.ID)
			}
			usedCodes[code] = struct{}{}
			rotated++
		}
		return nil
	})
	if err != nil {
		rotated = 0
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to rotate unclaimed profile codes"})
		return
	}

	if rotated > 0 {
		audit.RecordRequest(c, h.db, audit.Event{
			ViaRole: "admin", Action: "code.bulk_regenerate",
			EntityType: "student_code_rotation", EntityID: uuid.New(),
			Changes: map[string]any{"rotated_count": rotated},
		})
	}
	c.JSON(http.StatusOK, gin.H{"rotated_count": rotated})
}

func generateUnusedNumericCode(usedCodes map[string]struct{}) (string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		code, err := generateNumericCode(6)
		if err != nil {
			return "", err
		}
		if _, exists := usedCodes[code]; !exists {
			return code, nil
		}
	}
	return "", errors.New("could not generate a unique verification code")
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

	students[0].VerificationCode = ""
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
		Name       string     `json:"name"`
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
	// The student confirms their certificate name at signup; it replaces the
	// CR/admin-typed one. After this it's locked (see UpdateMyUser).
	if name := strings.Join(strings.Fields(req.Name), " "); name != "" {
		if len(name) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Name is too long"})
			return
		}
		updates["name"] = name
	}
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
	student.VerificationCode = ""
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
	student.VerificationCode = ""
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
	student.VerificationCode = ""
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
	student.VerificationCode = ""
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
