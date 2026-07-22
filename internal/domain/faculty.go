package domain

import "github.com/google/uuid"

// Faculty represents an academic faculty (e.g. "Faculty of Science") within
// a university. Departments optionally belong to one Faculty.
type Faculty struct {
	Base
	Name         string      `gorm:"size:255;not null" json:"name"`
	Slug         string      `gorm:"size:255;not null;index" json:"slug"`
	UniversityID uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	University   *University `json:"university,omitempty"`
}
