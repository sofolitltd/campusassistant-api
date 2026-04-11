package domain

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type Notification struct {
	Base
	UserID uuid.UUID       `gorm:"type:uuid;index" json:"user_id"`
	Title  string          `gorm:"size:255" json:"title"`
	Body   string          `gorm:"type:text" json:"body"`
	Type   string          `gorm:"size:50" json:"type"` // e.g. "NOTE_UPLOADED", "BATCH_NOTICE"
	IsRead bool            `gorm:"default:false" json:"is_read"`
	Data   *datatypes.JSON `gorm:"type:jsonb" json:"data,omitempty"` // Extra data for navigation
}
