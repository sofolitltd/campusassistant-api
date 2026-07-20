package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Notification is the content of a single "send" — one row per admin broadcast,
// regardless of how many users it was sent to.
type Notification struct {
	Base
	Title    string          `gorm:"size:255" json:"title"`
	Body     string          `gorm:"type:text" json:"body"`
	Type     string          `gorm:"size:50" json:"type"` // e.g. "NOTE_UPLOADED", "BATCH_NOTICE"
	Scope    string          `gorm:"size:20" json:"scope"` // "user" | "batch" | "department" | "university"
	TargetID *uuid.UUID      `gorm:"type:uuid" json:"target_id,omitempty"`
	Data     *datatypes.JSON `gorm:"type:jsonb" json:"data,omitempty"` // Extra data for navigation
}

// NotificationRecipient tracks per-user delivery/read state for a Notification.
type NotificationRecipient struct {
	Base
	NotificationID uuid.UUID  `gorm:"type:uuid;index;uniqueIndex:idx_notification_recipient" json:"notification_id"`
	UserID         uuid.UUID  `gorm:"type:uuid;index;uniqueIndex:idx_notification_recipient" json:"user_id"`
	IsRead         bool       `gorm:"default:false" json:"is_read"`
	ReadAt         *time.Time `json:"read_at,omitempty"`
}
