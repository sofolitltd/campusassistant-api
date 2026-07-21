package service

import (
	"context"
	"encoding/json"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/fcm"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NotificationService fans out notifications: one shared Notification content
// row plus a NotificationRecipient row per recipient, so callers never have
// to duplicate that transaction/batching logic themselves. If an FCM client
// is configured, it also best-effort pushes to each recipient's devices.
type NotificationService struct {
	db  *gorm.DB
	fcm *fcm.Client // nil if push isn't configured — push is silently skipped
}

func NewNotificationService(db *gorm.DB, fcmClient *fcm.Client) *NotificationService {
	return &NotificationService{db: db, fcm: fcmClient}
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

	if s.fcm != nil {
		// Fire-and-forget: push delivery must never fail or slow down
		// notification creation. Use context.Background() since the
		// request's ctx is cancelled once the HTTP response is written.
		go s.pushAsync(context.Background(), n, userIDs)
	}

	return &n, len(userIDs), nil
}

// pushAsync looks up device tokens for userIDs and sends n via FCM, pruning
// any tokens FCM reports as invalid/unregistered so dead installs stop being
// retried. Best-effort: all errors are logged, never propagated.
func (s *NotificationService) pushAsync(ctx context.Context, n domain.Notification, userIDs []uuid.UUID) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[fcm push] panic: %v", r)
		}
	}()

	var devices []domain.UserDevice
	if err := s.db.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&devices).Error; err != nil {
		logger.Errorf("[fcm push] failed to load devices: %v", err)
		return
	}
	if len(devices) == 0 {
		return
	}

	tokens := make([]string, len(devices))
	for i, d := range devices {
		tokens[i] = d.FCMToken
	}

	data := map[string]string{
		"notification_id": n.ID.String(),
		"type":             n.Type,
	}
	if n.Data != nil {
		var raw map[string]interface{}
		if err := json.Unmarshal(*n.Data, &raw); err == nil {
			if actionRoute, ok := raw["action_route"].(string); ok {
				data["action_route"] = actionRoute
			}
		}
	}

	result, err := s.fcm.Send(ctx, tokens, n.Title, n.Body, n.ImageURL, data)
	if err != nil {
		logger.Errorf("[fcm push] send failed for notification %s (%d tokens): %v", n.ID, len(tokens), err)
		return
	}
	logger.Infof("[fcm push] sent notification %s to %d token(s): success=%d failure=%d", n.ID, len(tokens), result.SuccessCount, result.FailureCount)

	if len(result.InvalidTokens) > 0 {
		if err := s.db.WithContext(ctx).Where("fcm_token IN ?", result.InvalidTokens).Delete(&domain.UserDevice{}).Error; err != nil {
			logger.Errorf("[fcm push] failed to prune invalid tokens: %v", err)
		}
	}
}

// SendToUsersViaTopics behaves like SendToUsers (same content row + one
// NotificationRecipient per user for the in-app inbox/read-state) but
// delivers push via FCM topic publishes — one per entry in topics — instead
// of resolving each user's device tokens and multicasting. Used for
// broadcast scopes (batch/department/university/custom) where recipients are
// defined by topic membership rather than an explicit token list, so there's
// no need to fan out per-token. A custom multi-target send may imply several
// topics (one per distinct target row); each gets its own FCM publish call,
// still just one DB write for the notification/recipients.
func (s *NotificationService) SendToUsersViaTopics(ctx context.Context, n domain.Notification, userIDs []uuid.UUID, createdBy uuid.UUID, topics []string) (*domain.Notification, int, error) {
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

	if s.fcm != nil {
		go s.pushTopicsAsync(context.Background(), n, topics)
	}

	return &n, len(userIDs), nil
}

// pushTopicsAsync sends n to every device subscribed to each topic in topics,
// one FCM publish call per topic. Best-effort: all errors are logged, never
// propagated.
func (s *NotificationService) pushTopicsAsync(ctx context.Context, n domain.Notification, topics []string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[fcm topic push] panic: %v", r)
		}
	}()

	data := map[string]string{
		"notification_id": n.ID.String(),
		"type":             n.Type,
	}
	if n.Data != nil {
		var raw map[string]interface{}
		if err := json.Unmarshal(*n.Data, &raw); err == nil {
			if actionRoute, ok := raw["action_route"].(string); ok {
				data["action_route"] = actionRoute
			}
		}
	}

	for _, topic := range topics {
		s.pushOneTopicAsync(ctx, n, topic, data)
	}
}

func (s *NotificationService) pushOneTopicAsync(ctx context.Context, n domain.Notification, topic string, data map[string]string) {
	messageID, err := s.fcm.SendToTopic(ctx, topic, n.Title, n.Body, n.ImageURL, data)
	if err != nil {
		logger.Errorf("[fcm topic push] send to %s failed for notification %s: %v", topic, n.ID, err)
		return
	}
	logger.Infof("[fcm topic push] sent notification %s to topic %s (message_id=%s)", n.ID, topic, messageID)
}
