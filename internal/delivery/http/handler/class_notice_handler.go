package handler

import (
	"net/http"
	"strings"
	"time"

	"campusassistant-api/internal/audit"
	"campusassistant-api/internal/authz"
	"campusassistant-api/internal/delivery/http/middleware"
	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClassNoticeHandler serves batch-scoped class notices. Reads are open to any
// signed-in user; writes are limited to admins and moderators with a matching
// batch, department, or university assignment.
type ClassNoticeHandler struct {
	db *gorm.DB
}

func NewClassNoticeHandler(db *gorm.DB) *ClassNoticeHandler {
	return &ClassNoticeHandler{db: db}
}

type classNoticeInput struct {
	UniversityID uuid.UUID  `json:"university_id"`
	DepartmentID uuid.UUID  `json:"department_id"`
	BatchID      *uuid.UUID `json:"batch_id"`
	Title        string     `json:"title"`
	Message      string     `json:"message"`
	FileURL      string     `json:"file_url"`
	FileType     string     `json:"file_type"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

func (in *classNoticeInput) validate() string {
	in.Title = strings.TrimSpace(in.Title)
	in.Message = strings.TrimSpace(in.Message)
	if in.Title == "" {
		return "Title is required"
	}
	if in.FileURL != "" && in.FileType != "image" && in.FileType != "pdf" {
		return "file_type must be 'image' or 'pdf'"
	}
	if in.FileURL == "" {
		in.FileType = ""
	}
	return ""
}

// activeCRBatch returns the batch the user is currently CR of in the given
// department, or false when they are not an active CR there.
func callerID(c *gin.Context) uuid.UUID {
	id, _ := c.Get("user_id")
	uid, _ := id.(uuid.UUID)
	return uid
}

func callerIsAdmin(c *gin.Context) bool {
	role, _ := c.Get("user_role")
	r, _ := role.(string)
	return middleware.IsAdminRole(r)
}

// authorize decides whether the caller may write to (department, batch) and
// returns the batch the notice must be pinned to.
func (h *ClassNoticeHandler) authorize(c *gin.Context, universityID, departmentID uuid.UUID, requested *uuid.UUID) (batch *uuid.UUID, name string, ok bool) {
	if callerIsAdmin(c) {
		return requested, "Admin", true
	}
	if requested == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Moderators must select a batch"})
		return nil, "", false
	}
	var targetBatch domain.Batch
	if err := h.db.WithContext(c.Request.Context()).First(&targetBatch, "id = ?", *requested).Error; err != nil ||
		targetBatch.DepartmentID != departmentID || targetBatch.UniversityID != universityID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Batch does not belong to this department and university"})
		return nil, "", false
	}
	allowed, err := authz.Can(c.Request.Context(), h.db, authz.Actor{UserID: callerID(c)}, authz.ClassNoticesManage, authz.Target{
		UniversityID: targetBatch.UniversityID,
		DepartmentID: targetBatch.DepartmentID,
		BatchID:      targetBatch.ID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return nil, "", false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot manage notices for this batch"})
		return nil, "", false
	}
	b := targetBatch.ID
	return &b, "Moderator", true
}

// List returns non-expired notices for a department. With batch_id it returns
// that batch's notices plus department-wide ones; include_expired=true (admin
// moderation) keeps expired rows.
func (h *ClassNoticeHandler) List(c *gin.Context) {
	deptID, err := uuid.Parse(c.Query("department_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "department_id is required"})
		return
	}
	q := h.db.Where("department_id = ?", deptID)
	if b := c.Query("batch_id"); b != "" {
		batchID, err := uuid.Parse(b)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid batch_id"})
			return
		}
		q = q.Where("(batch_id = ? OR batch_id IS NULL)", batchID)
	}
	if !(c.Query("include_expired") == "true" && callerIsAdmin(c)) {
		q = q.Where("(expires_at IS NULL OR expires_at > ?)", time.Now())
	}

	var items []domain.ClassNotice
	if err := q.Order("created_at DESC").Limit(100).Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load class notices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ClassNoticeHandler) Create(c *gin.Context) {
	var in classNoticeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	batch, name, ok := h.authorize(c, in.UniversityID, in.DepartmentID, in.BatchID)
	if !ok {
		return
	}

	n := domain.ClassNotice{
		UniversityID: in.UniversityID,
		DepartmentID: in.DepartmentID,
		BatchID:      batch,
		Title:        in.Title,
		Message:      in.Message,
		FileURL:      in.FileURL,
		FileType:     in.FileType,
		ExpiresAt:    in.ExpiresAt,
		PostedByName: name,
	}
	n.SetCreatedBy(callerID(c))
	n.SetUpdatedBy(callerID(c))
	if err := h.db.Create(&n).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create class notice"})
		return
	}
	audit.RecordRequest(c, h.db, audit.Event{
		ViaRole: moderatorAuditRole(c, h.db, n.UniversityID, n.DepartmentID, valueOrNil(n.BatchID)),
		Action:  "class_notice.create", EntityType: "class_notice", EntityID: n.ID,
		UniversityID: &n.UniversityID, DepartmentID: &n.DepartmentID, BatchID: n.BatchID,
		Changes: map[string]any{"created": n},
	})
	c.JSON(http.StatusCreated, n)
}

// load fetches the notice and checks the caller may modify it: an admin, or
// the CR who is still current for that department+batch.
func (h *ClassNoticeHandler) load(c *gin.Context) (domain.ClassNotice, bool) {
	var n domain.ClassNotice
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return n, false
	}
	if err := h.db.First(&n, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return n, false
	}
	if callerIsAdmin(c) {
		return n, true
	}
	if n.BatchID == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot manage this department-wide notice"})
		return n, false
	}
	allowed, err := authz.Can(c.Request.Context(), h.db, authz.Actor{UserID: callerID(c)}, authz.ClassNoticesManage, authz.Target{
		UniversityID: n.UniversityID, DepartmentID: n.DepartmentID, BatchID: *n.BatchID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify moderator permissions"})
		return n, false
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot manage notices for this batch"})
		return n, false
	}
	return n, true
}

func (h *ClassNoticeHandler) Update(c *gin.Context) {
	n, ok := h.load(c)
	if !ok {
		return
	}
	var in classNoticeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	// Scope (university/department/batch) is immutable after creation.
	n.Title, n.Message = in.Title, in.Message
	n.FileURL, n.FileType, n.ExpiresAt = in.FileURL, in.FileType, in.ExpiresAt
	n.SetUpdatedBy(callerID(c))
	if err := h.db.Save(&n).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update class notice"})
		return
	}
	audit.RecordRequest(c, h.db, audit.Event{
		ViaRole: moderatorAuditRole(c, h.db, n.UniversityID, n.DepartmentID, valueOrNil(n.BatchID)),
		Action:  "class_notice.update", EntityType: "class_notice", EntityID: n.ID,
		UniversityID: &n.UniversityID, DepartmentID: &n.DepartmentID, BatchID: n.BatchID,
		Changes: map[string]any{"updated": n},
	})
	c.JSON(http.StatusOK, n)
}

func (h *ClassNoticeHandler) Delete(c *gin.Context) {
	n, ok := h.load(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&n).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete class notice"})
		return
	}
	audit.RecordRequest(c, h.db, audit.Event{
		ViaRole: moderatorAuditRole(c, h.db, n.UniversityID, n.DepartmentID, valueOrNil(n.BatchID)),
		Action:  "class_notice.delete", EntityType: "class_notice", EntityID: n.ID,
		UniversityID: &n.UniversityID, DepartmentID: &n.DepartmentID, BatchID: n.BatchID,
		Changes: map[string]any{"deleted": n},
	})
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

func valueOrNil(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
