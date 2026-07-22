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

// notifyClubFollowers sends a push notification to every follower of
// clubID. Shared by ClubEventHandler (new event) and ClubManageHandler
// (new post) — both are "something happened in a club, tell the people
// who asked to be told" pushes. A no-op if there are zero followers;
// NotificationService.SendToUsers already short-circuits on an empty
// slice, so no empty Notification row is ever created.
func notifyClubFollowers(ctx context.Context, db *gorm.DB, notificationService *service.NotificationService, clubID uuid.UUID, title, body string, actorID uuid.UUID) error {
	var followerIDs []uuid.UUID
	if err := db.WithContext(ctx).Table("club_follows").
		Where("club_id = ?", clubID).
		Pluck("user_id", &followerIDs).Error; err != nil {
		return err
	}
	if len(followerIDs) == 0 {
		return nil
	}

	// Matches the Flutter app's actual GoRouter path (singular "club",
	// nested under /club) — NOT "/clubs/...".
	actionRoute := fmt.Sprintf("/club/%s", clubID)
	raw, err := json.Marshal(map[string]interface{}{
		"action_route": actionRoute,
		"club_id":      clubID,
	})
	if err != nil {
		return err
	}
	dataJSON := datatypes.JSON(raw)

	n := domain.Notification{
		Title: title,
		Body:  body,
		Type:  "club",
		Scope: "club",
		Data:  &dataJSON,
	}
	_, _, err = notificationService.SendToUsers(ctx, n, followerIDs, actorID)
	return err
}
