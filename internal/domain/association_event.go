package domain

import (
	"time"

	"github.com/google/uuid"
)

// AssociationEvent mirrors ClubEvent — a calendared event belonging to a
// single Association.
type AssociationEvent struct {
	Base
	AssociationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"association_id"`
	Title         string     `gorm:"size:255;not null" json:"title"`
	Description   string     `gorm:"type:text" json:"description"`
	ImageURL      string     `gorm:"size:500" json:"image_url"`
	Location      string     `gorm:"size:255" json:"location"`
	StartAt       time.Time  `gorm:"not null;index" json:"start_at"`
	EndAt         *time.Time `json:"end_at,omitempty"`
	IsPublished   bool       `gorm:"default:false" json:"is_published"`
}
