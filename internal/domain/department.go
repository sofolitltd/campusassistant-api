package domain

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Department represents a department within a university.
type Department struct {
	Base
	Name            string          `gorm:"size:255;not null" json:"name"`
	Acronym         string          `gorm:"size:20;index" json:"acronym"`        // e.g., CSE, EEE
	Slug            string          `gorm:"size:255;not null;index" json:"slug"` // e.g., psychology, cse
	EstablishedYear int             `json:"established_year"`
	About           string          `gorm:"type:text" json:"about"`
	WebsiteURL      string          `gorm:"size:255" json:"website_url"`
	LogoURL         string          `json:"logo_url"`
	Gallery         *datatypes.JSON `gorm:"type:jsonb" json:"gallery,omitempty"`
	UniversityID    uuid.UUID       `gorm:"type:uuid;not null;index" json:"university_id"`
	University      *University     `gorm:"foreignKey:UniversityID" json:"university,omitempty"`
	Batches         []Batch         `gorm:"foreignKey:DepartmentID" json:"batches,omitempty"`
}
