package service

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NotificationService fans out notifications: one shared Notification content
// row plus a NotificationRecipient row per recipient, so callers never have
// to duplicate that transaction/batching logic themselves.
type NotificationService struct {
	db *gorm.DB
}

func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{db: db}
}

const recipientBatchSize = 100

// SendToUsers creates n as a single content row and inserts one
// NotificationRecipient per user, atomically. No-ops (returns nil, 0, nil)
// if userIDs is empty. n.ID/CreatedByID/UpdatedByID are set by this call.
func (s *NotificationService) SendToUsers(ctx context.Context, n domain.Notification, userIDs []uuid.UUID, createdBy uuid.UUID) (*domain.Notification, int, error) {
	if len(userIDs) == 0 {
		return nil, 0, nil
	}

	n.ID = uuid.New()
	n.CreatedByID = createdBy
	n.UpdatedByID = createdBy

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&n).Error; err != nil {
			return err
		}

		recipients := make([]domain.NotificationRecipient, 0, len(userIDs))
		for _, uid := range userIDs {
			r := domain.NotificationRecipient{
				NotificationID: n.ID,
				UserID:         uid,
			}
			r.ID = uuid.New()
			r.CreatedByID = createdBy
			r.UpdatedByID = createdBy
			recipients = append(recipients, r)
		}

		for i := 0; i < len(recipients); i += recipientBatchSize {
			end := i + recipientBatchSize
			if end > len(recipients) {
				end = len(recipients)
			}
			if err := tx.Create(recipients[i:end]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return &n, len(userIDs), nil
}
