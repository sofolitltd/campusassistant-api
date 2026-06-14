package domain

import (
	"errors"
	"regexp"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Session represents an academic session (e.g., 2023-2024).
type Session struct {
	Base
	Name         string    `gorm:"size:50;not null" json:"name"`       // e.g. "2019-2020"
	Slug         string    `gorm:"size:50;not null;index" json:"slug"` // e.g. "19-20"
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"` // Scoped if needed
	IsActive     bool      `gorm:"default:true" json:"is_active"`
}

// BeforeSave hook for Session validation
func (s *Session) BeforeSave(tx *gorm.DB) (err error) {
	// Validate Name: Expected YYYY-YYYY (e.g., 2019-2020)
	nameRegex := regexp.MustCompile(`^\d{4}-\d{4}$`)
	if !nameRegex.MatchString(s.Name) {
		return errors.New("invalid session name format: expected YYYY-YYYY (e.g., 2019-2020)")
	}

	// Validate Slug: Expected YYYY-YYYY (e.g., 2019-2020)
	slugRegex := regexp.MustCompile(`^\d{4}-\d{4}$`)
	if !slugRegex.MatchString(s.Slug) {
		return errors.New("invalid session slug format: expected YYYY-YYYY (e.g., 2019-2020)")
	}

	return nil
}

