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

// DeviceTopicService keeps each UserDevice's FCM topic subscriptions in sync
// with the owning user's university/department/batch, so broadcast
// notifications can be sent as a single topic publish instead of a per-token
// multicast. Reconciliation is server-driven (not client-driven) so it stays
// correct even if a device is never cleanly logged out — e.g. a different
// user logging into the same physical device gets the previous owner's
// topics dropped and their own applied on next registration, regardless of
// what the client did.
type DeviceTopicService struct {
	db  *gorm.DB
	fcm *fcm.Client // nil if push isn't configured — reconciliation is silently skipped
}

func NewDeviceTopicService(db *gorm.DB, fcmClient *fcm.Client) *DeviceTopicService {
	return &DeviceTopicService{db: db, fcm: fcmClient}
}

const allTopic = "all"

func decodeTopics(raw string) []string {
	if raw == "" {
		return nil
	}
	var topics []string
	if err := json.Unmarshal([]byte(raw), &topics); err != nil {
		return nil
	}
	return topics
}

func encodeTopics(topics []string) string {
	raw, err := json.Marshal(topics)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func stringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

// desiredTopics computes the topic set userID should be subscribed to, based
// on their Student profile if present (most specific), falling back to the
// University/Department carried directly on User for non-student accounts
// (staff/teacher/admin) which have no batch.
func (s *DeviceTopicService) desiredTopics(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var user domain.User
	if err := s.db.WithContext(ctx).Preload("Student").First(&user, "id = ?", userID).Error; err != nil {
		return nil, err
	}

	topics := []string{allTopic}

	if user.Student != nil {
		topics = append(topics,
			"university_"+user.Student.UniversityID.String(),
			"department_"+user.Student.DepartmentID.String(),
			"batch_"+user.Student.BatchID.String(),
		)
		return topics, nil
	}

	if user.UniversityID != nil {
		topics = append(topics, "university_"+user.UniversityID.String())
	}
	if user.DepartmentID != nil {
		topics = append(topics, "department_"+user.DepartmentID.String())
	}
	return topics, nil
}

// ReconcileTopics diffs fcmToken's currently-recorded topics against what
// userID should now be subscribed to, subscribes/unsubscribes the deltas via
// FCM, and persists the new set. Best-effort: errors are logged, never
// returned to a caller expecting this to block a response.
func (s *DeviceTopicService) ReconcileTopics(ctx context.Context, userID uuid.UUID, fcmToken string) {
	if s.fcm == nil {
		return
	}

	desired, err := s.desiredTopics(ctx, userID)
	if err != nil {
		logger.Errorf("[fcm topics] failed to compute desired topics for user %s: %v", userID, err)
		return
	}

	var device domain.UserDevice
	if err := s.db.WithContext(ctx).Where("fcm_token = ?", fcmToken).First(&device).Error; err != nil {
		logger.Errorf("[fcm topics] failed to load device for token: %v", err)
		return
	}
	current := decodeTopics(device.SubscribedTopics)

	desiredSet := stringSet(desired)
	currentSet := stringSet(current)

	var toSubscribe, toUnsubscribe []string
	for _, topic := range desired {
		if _, ok := currentSet[topic]; !ok {
			toSubscribe = append(toSubscribe, topic)
		}
	}
	for _, topic := range current {
		if _, ok := desiredSet[topic]; !ok {
			toUnsubscribe = append(toUnsubscribe, topic)
		}
	}

	tokens := []string{fcmToken}
	for _, topic := range toSubscribe {
		resp, err := s.fcm.SubscribeToTopic(ctx, tokens, topic)
		if err != nil {
			logger.Errorf("[fcm topics] subscribe %s to %s failed: %v", userID, topic, err)
			continue
		}
		logger.Infof("[fcm topics] subscribed %s to %s (success=%d failure=%d)", userID, topic, resp.SuccessCount, resp.FailureCount)
	}
	for _, topic := range toUnsubscribe {
		resp, err := s.fcm.UnsubscribeFromTopic(ctx, tokens, topic)
		if err != nil {
			logger.Errorf("[fcm topics] unsubscribe %s from %s failed: %v", userID, topic, err)
			continue
		}
		logger.Infof("[fcm topics] unsubscribed %s from %s (success=%d failure=%d)", userID, topic, resp.SuccessCount, resp.FailureCount)
	}

	if err := s.db.WithContext(ctx).Model(&domain.UserDevice{}).
		Where("fcm_token = ?", fcmToken).
		Update("subscribed_topics", encodeTopics(desired)).Error; err != nil {
		logger.Errorf("[fcm topics] failed to persist topics for token: %v", err)
	}
}

// UnsubscribeTokens unsubscribes every token in tokens from the union of
// topics currently recorded against any of them, one FCM call per topic
// covering all tokens at once. Called before device rows are deleted
// (unregister/remove/logout-all/logout-others). Best-effort: never blocks
// the caller's delete on FCM's response.
func (s *DeviceTopicService) UnsubscribeTokens(ctx context.Context, tokens []string) {
	if s.fcm == nil || len(tokens) == 0 {
		return
	}

	var devices []domain.UserDevice
	if err := s.db.WithContext(ctx).Where("fcm_token IN ?", tokens).Find(&devices).Error; err != nil {
		logger.Errorf("[fcm topics] failed to load devices for unsubscribe: %v", err)
		return
	}

	topicSet := make(map[string]struct{})
	for _, d := range devices {
		for _, topic := range decodeTopics(d.SubscribedTopics) {
			topicSet[topic] = struct{}{}
		}
	}

	for topic := range topicSet {
		resp, err := s.fcm.UnsubscribeFromTopic(ctx, tokens, topic)
		if err != nil {
			logger.Errorf("[fcm topics] unsubscribe %d token(s) from %s failed: %v", len(tokens), topic, err)
			continue
		}
		logger.Infof("[fcm topics] unsubscribed %d token(s) from %s (success=%d failure=%d)", len(tokens), topic, resp.SuccessCount, resp.FailureCount)
	}
}
