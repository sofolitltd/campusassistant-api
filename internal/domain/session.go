package domain

import "github.com/google/uuid"

// Session represents an academic session (e.g., 2023-2024).
type Session struct {
	Base
	Name         string    `gorm:"size:50;not null" json:"name"`       // e.g. "2019-20"
	Slug         string    `gorm:"size:50;not null;index" json:"slug"` // e.g. "19-20"
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"` // Scoped if needed
	IsActive     bool      `gorm:"default:true" json:"is_active"`
}
