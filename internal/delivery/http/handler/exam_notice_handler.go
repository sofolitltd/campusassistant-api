package handler

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// ExamNoticeHandler is admin-only CRUD for ExamNotice, graduated out of the
// generic resource registration only because Create needs to fan out a push
// the moment an exam is posted — the generic handler has no hook for that.
type ExamNoticeHandler struct {
	db                  *gorm.DB
	notificationService *service.NotificationService
}

func NewExamNoticeHandler(db *gorm.DB, notificationService *service.NotificationService) *ExamNoticeHandler {
	return &ExamNoticeHandler{db: db, notificationService: notificationService}
}

type examNoticeInput struct {
	UniversityID uuid.UUID  `json:"university_id"`
	DepartmentID uuid.UUID  `json:"department_id"`
	BatchID      *uuid.UUID `json:"batch_id"`
	CourseName   string     `json:"course_name"`
	CourseCode   string     `json:"course_code"`
	ExamType     string     `json:"exam_type"`
	ExamDate     time.Time  `json:"exam_date"`
	StartTime    string     `json:"start_time"`
	Room         string     `json:"room"`
	SeatPlanURL  string     `json:"seat_plan_url"`
	SyllabusURL  string     `json:"syllabus_url"`
	Note         string     `json:"note"`
}

var validExamTypes = map[string]bool{
	"midterm": true, "final": true, "quiz": true, "retake": true, "improvement": true,
}

func (in *examNoticeInput) validate() string {
	in.CourseName = strings.TrimSpace(in.CourseName)
	if in.CourseName == "" {
		return "Course name is required"
	}
	if in.ExamDate.IsZero() {
		return "Exam date is required"
	}
	if !validExamTypes[in.ExamType] {
		return "Invalid exam_type"
	}
	return ""
}

func (h *ExamNoticeHandler) List(c *gin.Context) {
	deptID, err := uuid.Parse(c.Query("department_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "department_id is required"})
		return
	}
	limit := 200
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	var items []domain.ExamNotice
	if err := h.db.Where("department_id = ?", deptID).Order("exam_date ASC").Limit(limit).Find(&items).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load exam notices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ExamNoticeHandler) Create(c *gin.Context) {
	var in examNoticeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	n := domain.ExamNotice{
		UniversityID: in.UniversityID,
		DepartmentID: in.DepartmentID,
		BatchID:      in.BatchID,
		CourseName:   in.CourseName,
		CourseCode:   in.CourseCode,
		ExamType:     in.ExamType,
		ExamDate:     in.ExamDate,
		StartTime:    in.StartTime,
		Room:         in.Room,
		SeatPlanURL:  in.SeatPlanURL,
		SyllabusURL:  in.SyllabusURL,
		Note:         in.Note,
	}
	n.SetCreatedBy(callerID(c))
	n.SetUpdatedBy(callerID(c))
	if err := h.db.Create(&n).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create exam notice"})
		return
	}

	go h.notifyNewExam(context.Background(), n, callerID(c))

	c.JSON(http.StatusCreated, n)
}

func (h *ExamNoticeHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}
	var n domain.ExamNotice
	if err := h.db.First(&n, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	var in examNoticeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := in.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	// Scope (university/department/batch) is immutable after creation, same
	// as ClassNotice — only the exam's own details can change.
	n.CourseName, n.CourseCode, n.ExamType = in.CourseName, in.CourseCode, in.ExamType
	n.ExamDate, n.StartTime, n.Room = in.ExamDate, in.StartTime, in.Room
	n.SeatPlanURL, n.SyllabusURL, n.Note = in.SeatPlanURL, in.SyllabusURL, in.Note
	n.SetUpdatedBy(callerID(c))
	if err := h.db.Save(&n).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update exam notice"})
		return
	}
	c.JSON(http.StatusOK, n)
}

func (h *ExamNoticeHandler) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}
	if err := h.db.Delete(&domain.ExamNotice{}, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete exam notice"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

// examRecipients returns the user IDs of students in the exam's batch (if
// scoped to one) or its whole department otherwise, plus the FCM topic that
// reaches the same audience without per-token resolution.
func (h *ExamNoticeHandler) examRecipients(ctx context.Context, n domain.ExamNotice) ([]uuid.UUID, string) {
	q := h.db.WithContext(ctx).Table("students").Where("user_id IS NOT NULL")
	var topic string
	if n.BatchID != nil {
		q = q.Where("batch_id = ?", *n.BatchID)
		topic = "batch_" + n.BatchID.String()
	} else {
		q = q.Where("department_id = ?", n.DepartmentID)
		topic = "department_" + n.DepartmentID.String()
	}
	var ids []uuid.UUID
	_ = q.Pluck("user_id", &ids).Error
	return ids, topic
}

// notifyNewExam best-effort pushes a "new exam posted" notification the
// moment an admin creates one — the day-before reminder is handled
// separately by ExamReminderScheduler.
func (h *ExamNoticeHandler) notifyNewExam(ctx context.Context, n domain.ExamNotice, createdBy uuid.UUID) {
	ids, topic := h.examRecipients(ctx, n)
	if len(ids) == 0 {
		return
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"action_route": "/exams",
		"exam_id":      n.ID,
	})
	dataJSON := datatypes.JSON(raw)
	note := domain.Notification{
		Title: fmt.Sprintf("New Exam: %s", n.CourseName),
		Body:  examNotificationBody(n, "is scheduled"),
		Type:  "exam_notice",
		Data:  &dataJSON,
	}
	_, _, _ = h.notificationService.SendToUsersViaTopics(ctx, note, ids, createdBy, []string{topic})
}

func examNotificationBody(n domain.ExamNotice, verb string) string {
	when := n.ExamDate.Format("2 Jan")
	if n.StartTime != "" {
		when += " at " + n.StartTime
	}
	body := fmt.Sprintf("%s %s on %s", n.CourseName, verb, when)
	if n.Room != "" {
		body += " in " + n.Room
	}
	return body
}
