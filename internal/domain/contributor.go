package domain

import (
	"github.com/google/uuid"
)

// Contributor is a person credited on the app's "Our Contributors" page,
// typically promoted from a Student record by an admin.
type Contributor struct {
	Base
	Name             string     `gorm:"size:150;not null" json:"name"`
	ImageURL         string     `json:"image_url"`
	Tier             string     `gorm:"size:50;not null" json:"tier"`
	UniversityID     uuid.UUID  `gorm:"type:uuid;index;not null" json:"university_id"`
	UniversityName   string     `gorm:"size:150" json:"university_name"`
	DepartmentID     uuid.UUID  `gorm:"type:uuid;index;not null" json:"department_id"`
	DepartmentName   string     `gorm:"size:150" json:"department_name"`
	Session          string     `gorm:"size:50" json:"session"`
	StudentProfileID *uuid.UUID `gorm:"type:uuid;index" json:"student_profile_id,omitempty"`
}
