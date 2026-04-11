package domain

import "github.com/google/uuid"

// CoursePrefix represents allowed subject codes for a department (e.g., PSY, CSE).
type CoursePrefix struct {
	Base
	Prefix       string    `gorm:"size:20;not null;index" json:"prefix"` // e.g. "PSY", "CSE"
	Description  string    `gorm:"size:255" json:"description"`          // e.g. "Psychology"
	DepartmentID uuid.UUID `gorm:"type:uuid;not null;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
}
