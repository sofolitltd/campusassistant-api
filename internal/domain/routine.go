package domain

import "github.com/google/uuid"

// Routine represents a class or exam routine.
type Routine struct {
	Base
	Title        string    `gorm:"size:255;not null" json:"title"`
	ImageURL     string    `gorm:"size:500;not null" json:"image_url"`
	Time         string    `gorm:"size:100" json:"time"` // Descriptive time or semester
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}
