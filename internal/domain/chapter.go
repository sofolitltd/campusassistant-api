package domain

import (
	"github.com/google/uuid"
)

type Chapter struct {
	Base
	CourseCode   string    `gorm:"size:50;not null;index" json:"course_code"`
	ChapterNo    int       `gorm:"not null" json:"chapter_no"`
	ChapterTitle string    `gorm:"size:255;not null" json:"chapter_title"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`

	Batches []Batch `gorm:"many2many:chapter_batches;save_associations:false" json:"batches,omitempty"`
}
