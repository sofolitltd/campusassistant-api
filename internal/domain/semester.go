package domain

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Semester handles the variety: 1st Year, 2nd Year, 1st Semester, etc.
type Semester struct {
	Base
	Name   string `gorm:"size:100;not null" json:"name"` // e.g. "1st Year", "2nd Semester"
	Order  int    `gorm:"default:0" json:"order"`        // For sorting
	Status string `gorm:"size:20;default:'active'" json:"status"`

	// Multi-tenancy
	DepartmentID uuid.UUID `gorm:"type:uuid;not null;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`

	// Metrics
	TotalCourses int     `json:"total_courses"`
	TotalCredits float64 `json:"total_credits"` // Using float for 0.5/1.5 credits
	TotalMarks   int     `json:"total_marks"`

	Batches  []Batch     `gorm:"many2many:semester_batches;" json:"batches,omitempty"`
	BatchIDs []uuid.UUID `gorm:"-" json:"batch_ids,omitempty"`
}

// BeforeSave hook to sync batches from batch_ids
func (s *Semester) BeforeSave(tx *gorm.DB) (err error) {
	if len(s.BatchIDs) > 0 {
		var batches []Batch
		if err := tx.Where("id IN ?", s.BatchIDs).Find(&batches).Error; err != nil {
			return err
		}
		s.Batches = batches
	}
	return nil
}
