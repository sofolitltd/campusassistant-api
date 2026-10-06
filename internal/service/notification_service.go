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

// onCommitted is invoked once a fan-out's rows are durably committed, with
// the persisted content row and its recipients. Callers that need to act on
// a *committed* row (chiefly: the WebSocket broadcast in
// notification_handler.go, which hands each client a NotificationRecipient.ID
// that a follow-up mark-as-read/delete call must actually find) pass one in;
// everything else can ignore it.
type onCommitted func(domain.Notification, []domain.NotificationRecipient)

// SendToUsers creates n as a single content row and inserts one
// NotificationRecipient per user. No-ops (returns nil, nil, nil) if userIDs
// is empty. n.ID/CreatedByID/UpdatedByID and every recipient's ID are
// generated here, client-side, before any DB call — so the returned values
// are fully known immediately and callers never have to wait on the write to
// get IDs to respond/broadcast with.
//
// The actual DB write (and push) happens in the background: at broadcast
// scope this can be one row per user in the system, and a synchronous
// transaction of that size would make the admin's request latency scale with
// audience size for no reason, since nothing in the row content depends on
// the write having happened. notifyFns[0], if given, only fires after the
// write actually commits — so anything gated on "the row exists" (the WS
// broadcast) still can't race ahead of it; a failed commit is logged and
// simply never fires it.
func (s *NotificationService) SendToUsers(ctx context.Context, n domain.Notification, userIDs []uuid.UUID, createdBy uuid.UUID, notifyFns ...onCommitted) (*domain.Notification, []domain.NotificationRecipient, error) {
	if len(userIDs) == 0 {
		return nil, nil, nil
	}

	n.ID = uuid.New()
	n.CreatedByID = createdBy
	n.UpdatedByID = createdBy

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

	go s.persistAndNotify(context.Background(), n, recipients, firstOrNil(notifyFns))
	if s.fcm != nil {
		go s.pushAsync(context.Background(), n, userIDs)
	}

	return &n, recipients, nil
}

func firstOrNil(fns []onCommitted) onCommitted {
	if len(fns) == 0 {
		return nil
	}
	return fns[0]
}

// persistAndNotify runs the transaction SendToUsers/SendToUsersViaTopics used
// to do inline, then calls notify (if given) once it has actually committed.
// Runs in its own goroutine with a background context — the HTTP request
// that triggered it has already been responded to by the time this runs.
func (s *NotificationService) persistAndNotify(ctx context.Context, n domain.Notification, recipients []domain.NotificationRecipient, notify onCommitted) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[notification persist] panic: %v", r)
		}
	}()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&n).Error; err != nil {
			return err
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
		// The admin's request already returned success (the row content
		// never depended on the write). This is the one place that failure
		// is now only visible in logs — same trade-off the codebase already
		// accepts for push delivery below.
		logger.Errorf("[notification persist] failed to persist notification %s (%d recipients): %v", n.ID, len(recipients), err)
		return
	}

	if notify != nil {
		notify(n, recipients)
	}
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
func (s *NotificationService) SendToUsersViaTopics(ctx context.Context, n domain.Notification, userIDs []uuid.UUID, createdBy uuid.UUID, topics []string, notifyFns ...onCommitted) (*domain.Notification, []domain.NotificationRecipient, error) {
	if len(userIDs) == 0 {
		return nil, nil, nil
	}

	n.ID = uuid.New()
	n.CreatedByID = createdBy
	n.UpdatedByID = createdBy

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

	go s.persistAndNotify(context.Background(), n, recipients, firstOrNil(notifyFns))
	if s.fcm != nil {
		go s.pushTopicsAsync(context.Background(), n, topics)
	}

	return &n, recipients, nil
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
