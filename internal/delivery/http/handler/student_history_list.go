package handler

import (
	"net/http"
	"strconv"
	"strings"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetAll lists students. With batch_id and include_history=true it lists the
// batch's whole membership: its current students plus those who have since
// moved on (a StudentEnrollment row for this batch, e.g. Honors students now
// in Masters), each shown as they were in this batch. Without the flag it is
// the plain generic list — promote, audiences and other callers keep seeing
// only current members.
//
// Supported with the flag: limit, offset, search, is_regular, is_claimed,
// preload. is_regular and the roll in search use the enrollment's values for
// former students.
func (h *StudentHandler) GetAll(c *gin.Context) {
	batchID, err := uuid.Parse(c.Query("batch_id"))
	if c.Query("include_history") != "true" || err != nil {
		h.GenericHandler.GetAll(c)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 {
		limit = 20
	} else if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	base := h.db.WithContext(c.Request.Context()).Model(&domain.Student{}).
		Joins("LEFT JOIN student_enrollments e ON e.student_id = students.id AND e.batch_id = ? AND e.deleted_at IS NULL", batchID).
		Where("students.batch_id = ? OR e.id IS NOT NULL", batchID)
	if v, err := strconv.ParseBool(c.Query("is_regular")); err == nil {
		base = base.Where("COALESCE(e.is_regular, students.is_regular) = ?", v)
	}
	if v, err := strconv.ParseBool(c.Query("is_claimed")); err == nil {
		base = base.Where("students.is_claimed = ?", v)
	}
	if s := strings.TrimSpace(c.Query("search")); s != "" {
		like := "%" + strings.ToLower(s) + "%"
		base = base.Where(
			"LOWER(students.name) LIKE ? OR LOWER(students.email) LIKE ? OR LOWER(students.phone) LIKE ? OR LOWER(COALESCE(e.roll, students.student_id)) LIKE ?",
			like, like, like, like,
		)
	}

	var count int64
	if err := base.Session(&gorm.Session{}).Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	preload := c.Query("preload") == "true" || c.Query("include_details") == "true"
	q := base.Session(&gorm.Session{}).Select("students.*")
	if preload {
		q = q.Preload(clause.Associations)
	}
	var students []domain.Student
	if err := q.Order("students.weight ASC, COALESCE(e.roll, students.student_id) ASC").
		Limit(limit).Offset(offset).Find(&students).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := h.overlayEnrollments(c, batchID, students, preload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for i := range students {
		h.BeforeRespond(c, &students[i])
	}
	c.JSON(http.StatusOK, gin.H{
		"data":   students,
		"count":  count,
		"limit":  limit,
		"offset": offset,
	})
}

// overlayEnrollments rewrites former members of batchID to how they were in
// it, and marks them with HistoryEnrollmentID / CurrentBatchID.
func (h *StudentHandler) overlayEnrollments(c *gin.Context, batchID uuid.UUID, students []domain.Student, preload bool) error {
	ids := make([]uuid.UUID, 0, len(students))
	for _, s := range students {
		if s.BatchID != batchID {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	q := h.db.WithContext(c.Request.Context()).Where("batch_id = ? AND student_id IN ?", batchID, ids)
	if preload {
		q = q.Preload("Batch").Preload("Session")
	}
	var rows []domain.StudentEnrollment
	if err := q.Find(&rows).Error; err != nil {
		return err
	}
	byStudent := make(map[uuid.UUID]domain.StudentEnrollment, len(rows))
	for _, r := range rows {
		byStudent[r.StudentID] = r
	}
	for i := range students {
		s := &students[i]
		e, ok := byStudent[s.ID]
		if !ok || s.BatchID == batchID {
			continue
		}
		current, enrollmentID := s.BatchID, e.ID
		s.CurrentBatchID = &current
		s.HistoryEnrollmentID = &enrollmentID
		s.BatchID, s.SessionID = e.BatchID, e.SessionID
		s.StudentID, s.IsCR, s.IsRegular = e.Roll, e.IsCR, e.IsRegular
		if preload {
			s.Batch, s.Session = e.Batch, e.Session
		}
	}
	return nil
}
