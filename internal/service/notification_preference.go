package service

import (
	"context"
	"strings"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FilterMutedRecipients drops any userID that has muted category from
// userIDs, unless notifType is "emergency" (case-insensitive) — emergency
// notifications always bypass every mute. Safe to call with an empty
// userIDs slice.
func FilterMutedRecipients(ctx context.Context, db *gorm.DB, userIDs []uuid.UUID, category, notifType string) ([]uuid.UUID, error) {
	if len(userIDs) == 0 || strings.EqualFold(notifType, "emergency") {
		return userIDs, nil
	}

	var mutedIDs []uuid.UUID
	if err := db.WithContext(ctx).
		Model(&domain.NotificationMute{}).
		Where("category = ? AND user_id IN ?", category, userIDs).
		Pluck("user_id", &mutedIDs).Error; err != nil {
		return nil, err
	}
	if len(mutedIDs) == 0 {
		return userIDs, nil
	}

	muted := make(map[uuid.UUID]struct{}, len(mutedIDs))
	for _, id := range mutedIDs {
		muted[id] = struct{}{}
	}

	filtered := make([]uuid.UUID, 0, len(userIDs))
	for _, id := range userIDs {
		if _, ok := muted[id]; !ok {
			filtered = append(filtered, id)
		}
	}
	return filtered, nil
}

// loadMutedCategorySet returns the set of category keys userID has muted,
// for use by DeviceTopicService when computing which geographic FCM topics
// (university/department/batch) a device should be subscribed to.
func loadMutedCategorySet(ctx context.Context, db *gorm.DB, userID uuid.UUID) (map[string]struct{}, error) {
	var categories []string
	if err := db.WithContext(ctx).
		Model(&domain.NotificationMute{}).
		Where("user_id = ?", userID).
		Pluck("category", &categories).Error; err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(categories))
	for _, c := range categories {
		set[c] = struct{}{}
	}
	return set, nil
}
