package domain

import "github.com/google/uuid"

// Hall represents a residential hall in a university.
type Hall struct {
	Base
	Name         string      `gorm:"size:255;not null" json:"name"`
	Slug         string      `gorm:"size:255;not null;index" json:"slug"`
	UniversityID uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	University   *University `json:"university,omitempty"`
}
