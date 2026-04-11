package domain

import "github.com/google/uuid"

// Batch represents a specific batch of students (e.g., "CSE - 25th Batch").
type Batch struct {
	Base
	Name         string      `gorm:"size:100;not null" json:"name"`       // e.g. "Batch 10"
	Slug         string      `gorm:"size:100;not null;index" json:"slug"` // e.g. "batch-10"
	IsStudying   bool        `gorm:"default:true" json:"is_studying"`     // "study" field in firestore
	DepartmentID uuid.UUID   `gorm:"type:uuid;not null;index" json:"department_id"`
	Department   *Department `json:"department,omitempty"`
	UniversityID uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	Sessions     []Session   `gorm:"many2many:batch_sessions;" json:"sessions,omitempty"`
	Semesters    []Semester  `gorm:"many2many:semester_batches;" json:"semesters,omitempty"`
	Students     []Student   `gorm:"foreignKey:BatchID" json:"students,omitempty"`
}
