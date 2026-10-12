package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StudentVerificationHandler serves the document-based student onboarding
// flow: a signed-in user without a Student profile submits a request, and an
// admin approves (linking or creating the Student) or rejects it.
type StudentVerificationHandler struct {
	db              *gorm.DB
	repo            domain.StudentVerificationRepository
	notificationSvc *service.NotificationService
}

func NewStudentVerificationHandler(db *gorm.DB, repo domain.StudentVerificationRepository, notificationSvc *service.NotificationService) *StudentVerificationHandler {
	return &StudentVerificationHandler{db: db, repo: repo, notificationSvc: notificationSvc}
}

// studentVerificationRoute is where the app shows the request's status.
const studentVerificationRoute = "/student-verification"

type submitStudentVerificationRequest struct {
	UniversityID          uuid.UUID `json:"university_id" binding:"required"`
	DepartmentID          uuid.UUID `json:"department_id" binding:"required"`
	BatchID               uuid.UUID `json:"batch_id" binding:"required"`
	SessionID             uuid.UUID `json:"session_id" binding:"required"`
	StudentID             string    `json:"student_id" binding:"required"`
	Name                  string    `json:"name" binding:"required"`
	Phone                 string    `json:"phone"`
	IDCardAttachmentID    uuid.UUID `json:"id_card_attachment_id" binding:"required"`
	SecondDocType         string    `json:"second_doc_type" binding:"required"`
	SecondDocAttachmentID uuid.UUID `json:"second_doc_attachment_id" binding:"required"`
}

// Submit — POST /my/student-verification
func (h *StudentVerificationHandler) Submit(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	ctx := c.Request.Context()

	var req submitStudentVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	req.Name = strings.TrimSpace(req.Name)
	req.Phone = strings.TrimSpace(req.Phone)
	if req.StudentID == "" || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name and roll are required"})
		return
	}
	if len(req.StudentID) > 50 || len(req.Name) > 100 || len(req.Phone) > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name, roll or phone is too long"})
		return
	}

	docType := domain.StudentVerificationDocType(req.SecondDocType)
	if docType != domain.StudentVerificationDocRegistrationCard && docType != domain.StudentVerificationDocCertificate {
		c.JSON(http.StatusBadRequest, gin.H{"error": "second_doc_type must be registration_card or certificate"})
		return
	}
	if req.IDCardAttachmentID == req.SecondDocAttachmentID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Please upload two different documents"})
		return
	}

	var studentCount int64
	h.db.WithContext(ctx).Model(&domain.Student{}).Where("user_id = ?", userID).Count(&studentCount)
	if studentCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Your account is already linked to a student profile"})
		return
	}
	var pendingCount int64
	h.db.WithContext(ctx).Model(&domain.StudentVerificationRequest{}).
		Where("user_id = ? AND status = ?", userID, domain.StudentVerificationPending).Count(&pendingCount)
	if pendingCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "You already have a request under review"})
		return
	}

	if msg := h.validatePlacement(c, req); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	for _, id := range []uuid.UUID{req.IDCardAttachmentID, req.SecondDocAttachmentID} {
		if !h.ownsVerificationDocument(c, userID, id) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid document upload"})
			return
		}
	}

	v := &domain.StudentVerificationRequest{
		UserID:                userID,
		UniversityID:          req.UniversityID,
		DepartmentID:          req.DepartmentID,
		BatchID:               req.BatchID,
		SessionID:             req.SessionID,
		StudentID:             req.StudentID,
		Name:                  req.Name,
		Phone:                 req.Phone,
		IDCardAttachmentID:    req.IDCardAttachmentID,
		SecondDocType:         docType,
		SecondDocAttachmentID: req.SecondDocAttachmentID,
		Status:                domain.StudentVerificationPending,
	}
	if err := h.repo.Create(ctx, v); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit request"})
		return
	}
	c.JSON(http.StatusCreated, v)
}

// validatePlacement checks the chosen university/department/batch/session
// exist and belong together. Returns a user-facing message, or "" when valid.
func (h *StudentVerificationHandler) validatePlacement(c *gin.Context, req submitStudentVerificationRequest) string {
	db := h.db.WithContext(c.Request.Context())

	var dept domain.Department
	if err := db.First(&dept, "id = ?", req.DepartmentID).Error; err != nil || dept.UniversityID != req.UniversityID {
		return "Selected department does not belong to the selected university"
	}
	var batch domain.Batch
	if err := db.First(&batch, "id = ?", req.BatchID).Error; err != nil || batch.DepartmentID != req.DepartmentID {
		return "Selected batch does not belong to the selected department"
	}
	var session domain.Session
	if err := db.First(&session, "id = ?", req.SessionID).Error; err != nil || session.UniversityID != req.UniversityID {
		return "Selected session does not belong to the selected university"
	}
	return ""
}

// ownsVerificationDocument reports whether id is a private attachment the
// user uploaded to the student-verification folder via /my/upload.
func (h *StudentVerificationHandler) ownsVerificationDocument(c *gin.Context, userID, id uuid.UUID) bool {
	var a domain.Attachment
	if err := h.db.WithContext(c.Request.Context()).First(&a, "id = ?", id).Error; err != nil {
		return false
	}
	return a.UploaderID != nil && *a.UploaderID == userID &&
		a.Visibility == "private" &&
		strings.HasPrefix(a.StorageKey, domain.StudentVerificationUploadFolder+"/")
}

// GetMine — GET /my/student-verification: the caller's latest request.
func (h *StudentVerificationHandler) GetMine(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	v, err := h.repo.GetLatestByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No verification request found"})
		return
	}
	c.JSON(http.StatusOK, v)
}

// Admin endpoints

// GetAll — GET /student-verifications?status=&university_id=&department_id=&limit=&offset=
func (h *StudentVerificationHandler) GetAll(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	filter := map[string]interface{}{}
	if status := c.Query("status"); status != "" {
		filter["status"] = status
	}
	for _, key := range []string{"university_id", "department_id"} {
		if raw := c.Query(key); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + key})
				return
			}
			filter[key] = id
		}
	}

	items, count, err := h.repo.GetAll(c.Request.Context(), filter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.StudentVerificationRequest{}
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "count": count, "limit": limit, "offset": offset})
}

// verificationMatch tells the reviewer what approving will do to the
// Student table.
type verificationMatch struct {
	// Type is "unclaimed" (link to Student), "conflict" (Student already
	// belongs to another user — approve is refused) or "none" (a new
	// Student will be created).
	Type    string          `json:"type"`
	Student *domain.Student `json:"student,omitempty"`
	// OtherPendingCount is how many other pending requests claim the same
	// roll in the same batch.
	OtherPendingCount int64 `json:"other_pending_count"`
}

// GetByID — GET /student-verifications/:id
func (h *StudentVerificationHandler) GetByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	v, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Verification request not found"})
		return
	}

	db := h.db.WithContext(c.Request.Context())
	match := verificationMatch{Type: "none"}
	var student domain.Student
	err = db.Preload("User").
		Where("batch_id = ? AND student_id = ?", v.BatchID, v.StudentID).
		First(&student).Error
	if err == nil {
		match.Student = &student
		switch {
		case v.LinkedStudentID != nil && *v.LinkedStudentID == student.ID:
			match.Type = "linked"
		case student.UserID != nil && *student.UserID != v.UserID:
			match.Type = "conflict"
		default:
			match.Type = "unclaimed"
		}
	}
	db.Model(&domain.StudentVerificationRequest{}).
		Where("id <> ? AND batch_id = ? AND student_id = ? AND status = ?", v.ID, v.BatchID, v.StudentID, domain.StudentVerificationPending).
		Count(&match.OtherPendingCount)

	c.JSON(http.StatusOK, gin.H{"request": v, "match": match})
}

// reviewError carries the HTTP status an approve-transaction failure maps to.
type reviewError struct {
	status  int
	message string
}

func (e *reviewError) Error() string { return e.message }

// Approve — PUT /student-verifications/:id/approve
func (h *StudentVerificationHandler) Approve(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	reviewerID := reviewerFromContext(c)

	var v domain.StudentVerificationRequest
	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&v, "id = ?", id).Error; err != nil {
			return &reviewError{http.StatusNotFound, "Verification request not found"}
		}
		if v.Status != domain.StudentVerificationPending {
			return &reviewError{http.StatusConflict, "This request has already been reviewed"}
		}

		var user domain.User
		if err := tx.First(&user, "id = ?", v.UserID).Error; err != nil {
			return &reviewError{http.StatusNotFound, "The requesting user no longer exists"}
		}
		var linkedCount int64
		tx.Model(&domain.Student{}).Where("user_id = ?", v.UserID).Count(&linkedCount)
		if linkedCount > 0 {
			return &reviewError{http.StatusConflict, "This user is already linked to a student profile"}
		}

		now := time.Now()
		var student domain.Student
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("batch_id = ? AND student_id = ?", v.BatchID, v.StudentID).
			First(&student).Error
		switch {
		case err == nil:
			if student.UserID != nil {
				return &reviewError{http.StatusConflict, "This roll is already claimed by another account"}
			}
			updates := map[string]interface{}{
				"user_id":           v.UserID,
				"is_claimed":        true,
				"claimed_at":        now,
				"verification_code": "",
			}
			if student.Phone == "" && v.Phone != "" {
				updates["phone"] = v.Phone
			}
			if student.Email == "" {
				updates["email"] = user.Email
			}
			if err := tx.Model(&student).Updates(updates).Error; err != nil {
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			student = domain.Student{
				UserID:       &v.UserID,
				StudentID:    v.StudentID,
				BatchID:      v.BatchID,
				DepartmentID: v.DepartmentID,
				UniversityID: v.UniversityID,
				SessionID:    v.SessionID,
				Name:         v.Name,
				Email:        user.Email,
				Phone:        v.Phone,
				IsRegular:    true,
				IsClaimed:    true,
				ClaimedAt:    &now,
			}
			if err := tx.Create(&student).Error; err != nil {
				return err
			}
		default:
			return err
		}

		if err := tx.Model(&domain.User{}).Where("id = ?", v.UserID).Updates(map[string]interface{}{
			"is_verified":   true,
			"university_id": v.UniversityID,
			"department_id": v.DepartmentID,
			"role":          domain.RoleStudent,
		}).Error; err != nil {
			return err
		}

		v.Status = domain.StudentVerificationApproved
		v.RejectionReason = ""
		v.ReviewedByID = reviewerID
		v.ReviewedAt = &now
		v.LinkedStudentID = &student.ID
		return tx.Model(&v).Select("status", "rejection_reason", "reviewed_by_id", "reviewed_at", "linked_student_id").Updates(&v).Error
	})
	if err != nil {
		var re *reviewError
		if errors.As(err, &re) {
			c.JSON(re.status, gin.H{"error": re.message})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to approve request"})
		return
	}

	h.notifyStatus(c, &v)
	c.JSON(http.StatusOK, v)
}

// Reject — PUT /student-verifications/:id/reject {reason}
func (h *StudentVerificationHandler) Reject(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A rejection reason is required"})
		return
	}

	now := time.Now()
	res := h.db.WithContext(c.Request.Context()).Model(&domain.StudentVerificationRequest{}).
		Where("id = ? AND status = ?", id, domain.StudentVerificationPending).
		Updates(map[string]interface{}{
			"status":           domain.StudentVerificationRejected,
			"rejection_reason": body.Reason,
			"reviewed_by_id":   reviewerFromContext(c),
			"reviewed_at":      now,
		})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject request"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Request not found or already reviewed"})
		return
	}

	v, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "Request rejected"})
		return
	}
	h.notifyStatus(c, v)
	c.JSON(http.StatusOK, v)
}

func reviewerFromContext(c *gin.Context) *uuid.UUID {
	if raw, ok := c.Get("user_id"); ok {
		if id, ok := raw.(uuid.UUID); ok {
			return &id
		}
	}
	return nil
}

// notifyStatus pushes the review outcome to the applicant. Not filtered
// through notification mutes — it is an account-status message.
func (h *StudentVerificationHandler) notifyStatus(c *gin.Context, v *domain.StudentVerificationRequest) {
	if h.notificationSvc == nil {
		return
	}
	title := "Student verification approved"
	body := "Your student profile is verified. Welcome to your batch!"
	if v.Status == domain.StudentVerificationRejected {
		title = "Student verification rejected"
		body = "Your verification request was not approved."
		if v.RejectionReason != "" {
			body += " Reason: " + v.RejectionReason
		}
	}
	raw, err := json.Marshal(map[string]interface{}{"action_route": studentVerificationRoute, "request_id": v.ID})
	if err != nil {
		return
	}
	data := datatypes.JSON(raw)
	n := domain.Notification{Title: title, Body: body, Type: "STUDENT_VERIFICATION", Scope: "user", Data: &data}
	_, _, _ = h.notificationSvc.SendToUsers(c.Request.Context(), n, []uuid.UUID{v.UserID}, uuid.Nil)
}
