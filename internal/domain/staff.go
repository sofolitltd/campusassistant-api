package domain

import (
	"time"

	"github.com/google/uuid"
)

// Staff represents a general staff member (office, lab, support).
type Staff struct {
	Base
	DepartmentID     uuid.UUID  `gorm:"type:uuid;index" json:"department_id,omitempty"`
	UniversityID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"university_id"`
	Name             string     `gorm:"size:100" json:"name"`
	Post             string     `gorm:"size:100" json:"post"` // e.g., Office Assistant, Lab Technician
	Mobile           string     `gorm:"size:20" json:"mobile"`
	ImageURL         string     `gorm:"size:500" json:"image_url"`
	Serial           int        `gorm:"default:0" json:"serial"` // Display order
	VerificationCode string     `gorm:"size:20;index" json:"verification_code"`
	IsClaimed        bool       `gorm:"default:false" json:"is_claimed"`
	ClaimedAt        *time.Time `json:"claimed_at,omitempty"`
}
