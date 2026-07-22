package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// notifyAssociationFollowers sends a push notification to every follower of
// associationID. Mirrors notifyClubFollowers — shared by
// AssociationEventHandler (new event) and AssociationManageHandler (new
// post).
func notifyAssociationFollowers(ctx context.Context, db *gorm.DB, notificationService *service.NotificationService, associationID uuid.UUID, title, body string, actorID uuid.UUID) error {
	var followerIDs []uuid.UUID
	if err := db.WithContext(ctx).Table("association_follows").
		Where("association_id = ?", associationID).
		Pluck("user_id", &followerIDs).Error; err != nil {
		return err
	}
	if len(followerIDs) == 0 {
		return nil
	}

	// Deep-link route naming presumed to mirror Club's singular "/club/:id"
	// convention ("/association/:id") — confirm against the actual Flutter
	// GoRouter once mobile-side work for this feature starts.
	actionRoute := fmt.Sprintf("/association/%s", associationID)
	raw, err := json.Marshal(map[string]interface{}{
		"action_route":   actionRoute,
		"association_id": associationID,
	})
	if err != nil {
		return err
	}
	dataJSON := datatypes.JSON(raw)

	n := domain.Notification{
		Title: title,
		Body:  body,
		Type:  "association",
		Scope: "association",
		Data:  &dataJSON,
	}
	_, _, err = notificationService.SendToUsers(ctx, n, followerIDs, actorID)
	return err
}
