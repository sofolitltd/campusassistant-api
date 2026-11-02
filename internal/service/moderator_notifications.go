package service

import (
	"context"
	"encoding/json"
	"fmt"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type ModeratorNotificationScope struct {
	Type domain.ModeratorScope
	ID   uuid.UUID
}

func NotifyModerators(ctx context.Context, db *gorm.DB, notifications *NotificationService, message domain.Notification, scopes []ModeratorNotificationScope, createdBy uuid.UUID) error {
	if notifications == nil {
		return nil
	}
	for _, scope := range scopes {
		var userIDs []uuid.UUID
		if err := domain.ActiveModerator(db.WithContext(ctx)).
			Where("scope_type = ? AND scope_id = ?", scope.Type, scope.ID).
			Distinct("user_id").Pluck("user_id", &userIDs).Error; err != nil {
			return fmt.Errorf("load %s moderators: %w", scope.Type, err)
		}
		if len(userIDs) == 0 {
			continue
		}
		_, _, err := notifications.SendToUsers(ctx, message, userIDs, createdBy)
		if err != nil {
			return fmt.Errorf("send moderator notification: %w", err)
		}
		return nil
	}
	return nil
}

func ModeratorNotificationData(route string) (*datatypes.JSON, error) {
	raw, err := json.Marshal(map[string]string{"action_route": route})
	if err != nil {
		return nil, err
	}
	data := datatypes.JSON(raw)
	return &data, nil
}
