package domain

import "github.com/google/uuid"

// CourseCategory represents a dynamic grouping for courses (e.g., Major, Related, Practical).
type CourseCategory struct {
	Base
	Name         string    `gorm:"size:100;not null" json:"name"`
	Order        int       `gorm:"default:0" json:"order"` // For sorting Major above Practical
	DepartmentID uuid.UUID `gorm:"type:uuid;not null;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
}
