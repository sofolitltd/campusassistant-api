package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ExamReminderScheduler polls hourly for exams happening tomorrow and sends
// a one-time "exam tomorrow" push for each, same delivery mechanism as
// ExamNoticeHandler's "new exam posted" push but fired by the calendar
// rather than by an admin action. ReminderSentAt is the de-dup guard — an
// hourly sweep would otherwise re-fire all day.
type ExamReminderScheduler struct {
	db                  *gorm.DB
	notificationService *NotificationService
	interval            time.Duration
}

func NewExamReminderScheduler(db *gorm.DB, notificationService *NotificationService) *ExamReminderScheduler {
	return &ExamReminderScheduler{db: db, notificationService: notificationService, interval: time.Hour}
}

// Start runs the poll loop in a background goroutine until ctx is cancelled.
func (s *ExamReminderScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		s.tick(ctx) // catch exams due within the first hour after boot
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *ExamReminderScheduler) tick(ctx context.Context) {
	tomorrow := time.Now().AddDate(0, 0, 1)
	dayStart := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, tomorrow.Location())
	dayEnd := dayStart.Add(24 * time.Hour)

	var due []domain.ExamNotice
	if err := s.db.WithContext(ctx).
		Where("exam_date >= ? AND exam_date < ?", dayStart, dayEnd).
		Where("reminder_sent_at IS NULL").
		Find(&due).Error; err != nil {
		log.Printf("ExamReminderScheduler: failed to query due exams: %v", err)
		return
	}

	for _, exam := range due {
		s.remind(ctx, exam)
		if err := s.db.WithContext(ctx).Model(&domain.ExamNotice{}).
			Where("id = ?", exam.ID).
			Update("reminder_sent_at", time.Now()).Error; err != nil {
			log.Printf("ExamReminderScheduler: failed to mark exam %s reminded: %v", exam.ID, err)
		}
	}
}

func (s *ExamReminderScheduler) remind(ctx context.Context, exam domain.ExamNotice) {
	q := s.db.WithContext(ctx).Table("students").Where("user_id IS NOT NULL")
	var topic string
	if exam.BatchID != nil {
		q = q.Where("batch_id = ?", *exam.BatchID)
		topic = "batch_" + exam.BatchID.String()
	} else {
		q = q.Where("department_id = ?", exam.DepartmentID)
		topic = "department_" + exam.DepartmentID.String()
	}
	var ids []uuid.UUID
	if err := q.Pluck("user_id", &ids).Error; err != nil {
		log.Printf("ExamReminderScheduler: failed to resolve recipients for exam %s: %v", exam.ID, err)
		return
	}
	if len(ids) == 0 {
		return
	}

	raw, err := json.Marshal(map[string]interface{}{
		"action_route": "/exams",
		"exam_id":      exam.ID,
	})
	if err != nil {
		return
	}
	dataJSON := datatypes.JSON(raw)

	when := exam.StartTime
	if when == "" {
		when = "tomorrow"
	} else {
		when = "tomorrow at " + when
	}
	n := domain.Notification{
		Title: fmt.Sprintf("Exam Tomorrow: %s", exam.CourseName),
		Body:  fmt.Sprintf("%s is %s%s", exam.CourseName, when, roomSuffix(exam.Room)),
		Type:  "exam_reminder",
		Data:  &dataJSON,
	}
	_, _, err = s.notificationService.SendToUsersViaTopics(ctx, n, ids, exam.CreatedByID, []string{topic})
	if err != nil {
		log.Printf("ExamReminderScheduler: failed to send reminder for exam %s: %v", exam.ID, err)
	}
}

func roomSuffix(room string) string {
	if room == "" {
		return ""
	}
	return " in " + room
}
