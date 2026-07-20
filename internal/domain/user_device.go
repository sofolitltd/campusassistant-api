package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserDevice is one FCM-registered app install. The token itself (not
// user_id+token) is the unique key: a token belongs to exactly one install,
// so registering the same token again just reassigns it to whoever is
// currently logged in on that device, instead of creating a duplicate row.
type UserDevice struct {
	Base
	UserID     uuid.UUID `gorm:"type:uuid;index" json:"user_id"`
	FCMToken   string    `gorm:"size:255;uniqueIndex" json:"fcm_token"`
	Platform   string    `gorm:"size:20" json:"platform"` // "android" | "ios" | "web"
	LastSeenAt time.Time `json:"last_seen_at"`
}
