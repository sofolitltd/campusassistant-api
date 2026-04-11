package domain

import (
	"github.com/google/uuid"
)

// Bookmark links a user to a specific resource or other entity.
type Bookmark struct {
	Base
	UserID     uuid.UUID `gorm:"type:uuid;index;not null" json:"user_id"`
	EntityType string    `gorm:"size:50;index;not null" json:"entity_type"` // e.g. "resource", "alumni", "teacher"
	EntityID   uuid.UUID `gorm:"type:uuid;index;not null" json:"entity_id"`
}
