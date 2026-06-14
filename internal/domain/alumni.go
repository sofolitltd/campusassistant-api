package domain

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Alumni represents a graduate of the university.
type Alumni struct {
	Base
	FullName      string         `gorm:"size:100;not null" json:"full_name"`
	StudentID     string         `gorm:"size:50" json:"student_id"`
	Email         string         `gorm:"size:100" json:"email"`
	Phone         string         `gorm:"size:20" json:"phone"`
	Batch         string         `gorm:"size:50" json:"batch"`
	PassingYear   string         `gorm:"size:20" json:"passing_year"`
	CurrentStatus string         `gorm:"size:100" json:"current_status"`
	Organization  string         `gorm:"size:100" json:"organization"`
	Designation   string         `gorm:"size:100" json:"designation"`
	Location         string         `gorm:"size:100" json:"location"`
	Bio              string         `gorm:"type:text" json:"bio"`
	ProfileImage     string         `json:"profile_image"`
	SocialLinks      datatypes.JSON `gorm:"type:jsonb" json:"social_links"` // e.g. {"facebook": "...", "linkedin": "..."}
	CreatedBy        uuid.UUID      `gorm:"type:uuid" json:"created_by"`
	UniversityID     uuid.UUID      `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID     uuid.UUID      `gorm:"type:uuid;index" json:"department_id"`
	StudentProfileID *uuid.UUID     `gorm:"type:uuid;index" json:"student_profile_id,omitempty"`
	StudentProfile   *Student       `gorm:"foreignKey:StudentProfileID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"student_profile,omitempty"`
	OrganizationID   *uuid.UUID     `gorm:"type:uuid;index" json:"organization_id,omitempty"`
	OrganizationRef  *Organization  `gorm:"foreignKey:OrganizationID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"organization_ref,omitempty"`
}

// BeforeSave updates the legacy Organization string with the associated Organization's name.
func (a *Alumni) BeforeSave(tx *gorm.DB) (err error) {
	if a.OrganizationID != nil && *a.OrganizationID != uuid.Nil {
		var org Organization
		if err := tx.First(&org, "id = ?", a.OrganizationID).Error; err == nil {
			a.Organization = org.Name
		}
	}
	return nil
}


