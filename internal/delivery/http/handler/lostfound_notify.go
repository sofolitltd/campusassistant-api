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

// notifyLostFoundUser pushes a single-user notification for something that
// happened on a Lost & Found item (new claim, claim accepted/rejected). Same
// shape as notifyClubFollowers, just single-recipient instead of fan-out.
func notifyLostFoundUser(ctx context.Context, db *gorm.DB, notificationService *service.NotificationService, userID uuid.UUID, title, body string, itemID uuid.UUID, actorID uuid.UUID) error {
	recipients, err := service.FilterMutedRecipients(ctx, db, []uuid.UUID{userID}, "lost_found", "lost_found")
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return nil
	}

	actionRoute := fmt.Sprintf("/lost-found/%s", itemID)
	raw, err := json.Marshal(map[string]interface{}{
		"action_route": actionRoute,
		"item_id":      itemID,
	})
	if err != nil {
		return err
	}
	dataJSON := datatypes.JSON(raw)

	n := domain.Notification{
		Title: title,
		Body:  body,
		Type:  "lost_found",
		Scope: "lost_found",
		Data:  &dataJSON,
	}
	_, _, err = notificationService.SendToUsers(ctx, n, recipients, actorID)
	return err
}
