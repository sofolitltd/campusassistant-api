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

// CareerReminderScheduler polls for due CareerReminders and delivers them as
// FCM push notifications, replacing the client-scheduled local-notification
// approach: this survives app reinstalls/reboots and works across a
// student's devices, at the cost of requiring connectivity at fire time
// (same trade-off as every other push in this app).
type CareerReminderScheduler struct {
	db                  *gorm.DB
	repo                domain.CareerRepository
	notificationService *NotificationService
	interval            time.Duration
	batchSize           int
}

func NewCareerReminderScheduler(db *gorm.DB, repo domain.CareerRepository, notificationService *NotificationService) *CareerReminderScheduler {
	return &CareerReminderScheduler{
		db:                  db,
		repo:                repo,
		notificationService: notificationService,
		interval:            30 * time.Second,
		batchSize:           100,
	}
}

// Start runs the poll loop in a background goroutine until ctx is cancelled.
func (s *CareerReminderScheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
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

func (s *CareerReminderScheduler) tick(ctx context.Context) {
	due, err := s.repo.ClaimDueReminders(ctx, time.Now(), s.batchSize)
	if err != nil {
		log.Printf("CareerReminderScheduler: failed to claim due reminders: %v", err)
		return
	}
	for _, reminder := range due {
		if err := s.deliver(ctx, reminder); err != nil {
			log.Printf("CareerReminderScheduler: failed to deliver reminder %s: %v", reminder.ID, err)
			continue
		}
		if err := s.repo.MarkReminderSent(ctx, reminder.ID); err != nil {
			log.Printf("CareerReminderScheduler: failed to mark reminder %s sent: %v", reminder.ID, err)
		}
	}
}

func (s *CareerReminderScheduler) deliver(ctx context.Context, reminder domain.CareerReminder) error {
	recipients, err := FilterMutedRecipients(ctx, s.db, []uuid.UUID{reminder.UserID}, "career", "career_reminder")
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return nil
	}

	actionRoute := "/career/reminders"
	if reminder.JobID != nil {
		actionRoute = fmt.Sprintf("/career/jobs/%s", *reminder.JobID)
	}
	raw, err := json.Marshal(map[string]interface{}{
		"action_route": actionRoute,
		"reminder_id":  reminder.ID,
	})
	if err != nil {
		return err
	}
	dataJSON := datatypes.JSON(raw)

	n := domain.Notification{
		Title: "Reminder",
		Body:  reminder.Title,
		Type:  "career_reminder",
		Scope: "user",
		Data:  &dataJSON,
	}
	_, _, err = s.notificationService.SendToUsers(ctx, n, recipients, reminder.UserID)
	return err
}
