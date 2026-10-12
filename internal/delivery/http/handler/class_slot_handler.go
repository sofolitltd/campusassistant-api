package handler

import (
	"net/http"
	"strings"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClassSlotHandler serves the weekly class schedule: recurring slots plus
// per-date status overrides (cancelled/rescheduled). Reads are open to any
// signed-in user; writes are limited to admins and to the *current* CR of
// the slot's own department + batch, enforced here from the database.
type ClassSlotHandler struct {
	db *gorm.DB
}

func NewClassSlotHandler(db *gorm.DB) *ClassSlotHandler {
	return &ClassSlotHandler{db: db}
}

func (h *ClassSlotHandler) activeCRBatch(userID, departmentID uuid.UUID) (cr domain.CR, ok bool) {
	err := h.db.Scopes(domain.ActiveCR, domain.CRsOf(userID)).
		Where("department_id = ?", departmentID).
		First(&cr).Error
	return cr, err == nil
}

// authorize decides whether the caller may write to (department, batch).
func (h *ClassSlotHandler) authorize(c *gin.Context, departmentID, batchID uuid.UUID) bool {
	if callerIsAdmin(c) {
		return true
	}
	cr, isCR := h.activeCRBatch(callerID(c), departmentID)
	if !isCR || cr.BatchID != batchID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the current CR of this batch can manage its class schedule"})
		return false
	}
	return true
}

type classSlotInput struct {
	UniversityID uuid.UUID `json:"university_id"`
	DepartmentID uuid.UUID `json:"department_id"`
	BatchID      uuid.UUID `json:"batch_id"`
	DayOfWeek    int       `json:"day_of_week"`
	CourseName   string    `json:"course_name"`
	Teacher      string    `json:"teacher"`
	Room         string    `json:"room"`
	StartTime    string    `json:"start_time"`
	EndTime      string    `json:"end_time"`
}

func (in *classSlotInput) validate() string {
	in.CourseName = strings.TrimSpace(in.CourseName)
	if in.CourseName == "" {
		return "course_name is required"
	}
	if in.DayOfWeek < 0 || in.DayOfWeek > 6 {
		return "day_of_week must be between 0 (Sunday) and 6 (Saturday)"
	}
	if in.StartTime == "" || in.EndTime == "" {
		return "start_time and end_time are required"
	}
	return ""
}

// List returns the weekly slots for a department, optionally narrowed to one batch.
func (h *ClassSlotHandler) List(c *gin.Context) {
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
		q = q.Where("batch_id = ?", batchID)
	}
	var items []domain.ClassSlot
	if err := q.Order("day_of_week ASC, start_time ASC").Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load class schedule"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ClassSlotHandler) Create(c *gin.Context) {
	var in classSlotInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if !h.authorize(c, in.DepartmentID, in.BatchID) {
		return
	}
	s := domain.ClassSlot{
		UniversityID: in.UniversityID,
		DepartmentID: in.DepartmentID,
		BatchID:      in.BatchID,
		DayOfWeek:    in.DayOfWeek,
		CourseName:   in.CourseName,
		Teacher:      in.Teacher,
		Room:         in.Room,
		StartTime:    in.StartTime,
		EndTime:      in.EndTime,
	}
	s.SetCreatedBy(callerID(c))
	s.SetUpdatedBy(callerID(c))
	if err := h.db.Create(&s).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create class slot"})
		return
	}
	c.JSON(http.StatusCreated, s)
}

func (h *ClassSlotHandler) load(c *gin.Context) (domain.ClassSlot, bool) {
	var s domain.ClassSlot
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return s, false
	}
	if err := h.db.First(&s, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return s, false
	}
	if !h.authorize(c, s.DepartmentID, s.BatchID) {
		return s, false
	}
	return s, true
}

func (h *ClassSlotHandler) Update(c *gin.Context) {
	s, ok := h.load(c)
	if !ok {
		return
	}
	var in classSlotInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	// Scope (university/department/batch) is immutable after creation.
	s.DayOfWeek, s.CourseName, s.Teacher = in.DayOfWeek, in.CourseName, in.Teacher
	s.Room, s.StartTime, s.EndTime = in.Room, in.StartTime, in.EndTime
	s.SetUpdatedBy(callerID(c))
	if err := h.db.Save(&s).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update class slot"})
		return
	}
	c.JSON(http.StatusOK, s)
}

func (h *ClassSlotHandler) Delete(c *gin.Context) {
	s, ok := h.load(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&s).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete class slot"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

// --- Status overrides (cancelled / rescheduled occurrences) ---

type classSlotStatusInput struct {
	ClassSlotID  uuid.UUID `json:"class_slot_id"`
	Date         string    `json:"date"` // "YYYY-MM-DD"
	Status       string    `json:"status"`
	NewStartTime string    `json:"new_start_time"`
	NewEndTime   string    `json:"new_end_time"`
	NewRoom      string    `json:"new_room"`
	Note         string    `json:"note"`
}

func (in *classSlotStatusInput) validate() (time.Time, string) {
	if in.Status != "cancelled" && in.Status != "rescheduled" {
		return time.Time{}, "status must be 'cancelled' or 'rescheduled'"
	}
	d, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return time.Time{}, "date must be in YYYY-MM-DD format"
	}
	return d, ""
}

// ListStatus returns overrides for a department (optionally one batch) on one date.
func (h *ClassSlotHandler) ListStatus(c *gin.Context) {
	deptID, err := uuid.Parse(c.Query("department_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "department_id is required"})
		return
	}
	date, err := time.Parse("2006-01-02", c.Query("date"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "date is required in YYYY-MM-DD format"})
		return
	}
	var slotIDs []uuid.UUID
	sq := h.db.Model(&domain.ClassSlot{}).Where("department_id = ?", deptID)
	if b := c.Query("batch_id"); b != "" {
		batchID, err := uuid.Parse(b)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid batch_id"})
			return
		}
		sq = sq.Where("batch_id = ?", batchID)
	}
	if err := sq.Pluck("id", &slotIDs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load class schedule"})
		return
	}
	var items []domain.ClassSlotStatus
	if err := h.db.Where("class_slot_id IN ? AND date = ?", slotIDs, date).Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load class statuses"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// SetStatus creates or replaces the override for a slot's occurrence on a date.
func (h *ClassSlotHandler) SetStatus(c *gin.Context) {
	var in classSlotStatusInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	date, msg := in.validate()
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	var slot domain.ClassSlot
	if err := h.db.First(&slot, "id = ?", in.ClassSlotID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Class slot not found"})
		return
	}
	if !h.authorize(c, slot.DepartmentID, slot.BatchID) {
		return
	}

	status := domain.ClassSlotStatus{
		ClassSlotID:  in.ClassSlotID,
		Date:         date,
		Status:       in.Status,
		NewStartTime: in.NewStartTime,
		NewEndTime:   in.NewEndTime,
		NewRoom:      in.NewRoom,
		Note:         in.Note,
	}
	var existing domain.ClassSlotStatus
	if err := h.db.Where("class_slot_id = ? AND date = ?", in.ClassSlotID, date).First(&existing).Error; err == nil {
		status.Base = existing.Base
		status.SetUpdatedBy(callerID(c))
		if err := h.db.Save(&status).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update class status"})
			return
		}
	} else {
		status.SetCreatedBy(callerID(c))
		status.SetUpdatedBy(callerID(c))
		if err := h.db.Create(&status).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create class status"})
			return
		}
	}
	c.JSON(http.StatusOK, status)
}

// DeleteStatus reverts an occurrence back to "held as scheduled".
func (h *ClassSlotHandler) DeleteStatus(c *gin.Context) {
	var status domain.ClassSlotStatus
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}
	if err := h.db.First(&status, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	var slot domain.ClassSlot
	if err := h.db.First(&slot, "id = ?", status.ClassSlotID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Class slot not found"})
		return
	}
	if !h.authorize(c, slot.DepartmentID, slot.BatchID) {
		return
	}
	if err := h.db.Delete(&status).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete class status"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}
