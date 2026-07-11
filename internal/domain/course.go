package domain

import (
	"github.com/google/uuid"
)

// Course represents a subject or course offered by a department.
// It uses FK references to CourseCategory and Level so renaming
// those entities never breaks existing course data.
type Course struct {
	Base
	CourseCode   string    `gorm:"size:50;not null;index" json:"course_code"`
	CourseTitle  string    `gorm:"size:255;not null" json:"course_title"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	TotalCredits float64   `gorm:"default:0" json:"total_credits"`
	TotalMarks   int       `gorm:"default:0" json:"total_marks"`
	ThumbnailURL string    `gorm:"size:500" json:"thumbnail_url"`

	// --- Enterprise FK References (rename-safe) ---

	// CourseCategoryID stores the UUID of the CourseCategory.
	// The joined Category object is returned in the API response.
	CourseCategoryID *uuid.UUID      `gorm:"type:uuid;index" json:"course_category_id"`
	CourseCategory   *CourseCategory `gorm:"foreignKey:CourseCategoryID" json:"course_category,omitempty"`

	// LevelID stores the UUID of the Level (academic year/term).
	// The joined Level object is returned in the API response.
	LevelID *uuid.UUID `gorm:"type:uuid;index" json:"level_id"`
	Level   *Level     `gorm:"foreignKey:LevelID" json:"level,omitempty"`

	BatchIDs []string `gorm:"-" json:"batch_ids,omitempty"`
	Batches  []Batch  `gorm:"many2many:course_batches;" json:"batches,omitempty"`
}
