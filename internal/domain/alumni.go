package domain

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Alumni represents a graduate of the university.
type Alumni struct {
	Base
	FullName      string         `gorm:"size:100;not null" json:"full_name"`
	StudentID     string         `gorm:"size:50;not null" json:"student_id"`
	Email         string         `gorm:"size:100" json:"email"`
	Phone         string         `gorm:"size:20" json:"phone"`
	Batch         string         `gorm:"size:50" json:"batch"`
	PassingYear   string         `gorm:"size:20" json:"passing_year"`
	CurrentStatus string         `gorm:"size:100" json:"current_status"`
	Organization  string         `gorm:"size:100" json:"organization"`
	Designation   string         `gorm:"size:100" json:"designation"`
	Location      string         `gorm:"size:100" json:"location"`
	Bio           string         `gorm:"type:text" json:"bio"`
	ProfileImage  string         `json:"profile_image"`
	SocialLinks   datatypes.JSON `gorm:"type:jsonb" json:"social_links"` // e.g. {"facebook": "...", "linkedin": "..."}
	CreatedBy     uuid.UUID      `gorm:"type:uuid" json:"created_by"`
	UniversityID  uuid.UUID      `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID  uuid.UUID      `gorm:"type:uuid;index" json:"department_id"`
}
