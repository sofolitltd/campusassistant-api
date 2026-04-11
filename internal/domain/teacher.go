package domain

import (
	"time"

	"github.com/google/uuid"
)

// Teacher represents a teacher profile.
type Teacher struct {
	Base
	UserID           *uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"user_id,omitempty"`
	User             *User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user,omitempty"`
	DepartmentID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"department_id"`
	UniversityID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"university_id"`
	Name             string     `gorm:"size:100" json:"name"`
	Email            string     `gorm:"size:100" json:"email"`
	Phone            string     `gorm:"size:20" json:"phone"`
	Designation      string     `gorm:"size:100" json:"designation"` 
	About            string     `gorm:"type:text" json:"about"`
	Interests        string     `gorm:"type:text" json:"interests"`
	PhD              string     `gorm:"size:255" json:"phd"`
	Publications     string     `gorm:"type:text" json:"publications"`
	IsChairman       bool       `gorm:"default:false" json:"is_chairman"`
	IsPresent        bool       `gorm:"default:true" json:"is_present"`
	Weight           int        `gorm:"default:0" json:"weight"`
	VerificationCode string     `gorm:"size:20;index" json:"verification_code"`
	IsClaimed        bool       `gorm:"default:false" json:"is_claimed"`
	ClaimedAt        *time.Time `json:"claimed_at,omitempty"`
}
