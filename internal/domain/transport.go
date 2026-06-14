package domain

import "github.com/google/uuid"

// Transport represents transport information (e.g. Bus Route schedule image).
type Transport struct {
	Base
	Title        string    `gorm:"size:255;not null;default:''" json:"title"`
	Image        string    `gorm:"size:500;not null;default:''" json:"image"` // mapped to json "image"
	Time         string    `gorm:"size:100" json:"time"`           // descriptive time/schedule
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
}

