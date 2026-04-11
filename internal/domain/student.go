package domain

import (
	"time"

	"github.com/google/uuid"
)

// Student represents a student profile.
type Student struct {
	Base
	UserID           *uuid.UUID  `gorm:"type:uuid;uniqueIndex" json:"user_id,omitempty"`
	User             *User       `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user,omitempty"`
	StudentID        string      `gorm:"size:50;index" json:"student_id"` // Firestore "id": "15608055"
	BatchID          uuid.UUID   `gorm:"type:uuid;not null;index" json:"batch_id"`
	Batch            *Batch      `json:"batch,omitempty"`
	DepartmentID     uuid.UUID   `gorm:"type:uuid;not null;index" json:"department_id"`
	Department       *Department `json:"department,omitempty"`
	UniversityID     uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	University       *University `json:"university,omitempty"`
	SessionID        uuid.UUID   `gorm:"type:uuid;not null;index" json:"session_id"`
	Session          *Session    `json:"session,omitempty"`
	HallID           *uuid.UUID  `gorm:"type:uuid;index" json:"hall_id,omitempty"`
	Hall             *Hall       `json:"hall,omitempty"`
	Name             string      `gorm:"size:100" json:"name"`
	Email            string      `gorm:"size:100" json:"email"`
	Phone            string      `gorm:"size:20" json:"phone"`
	IsRegular        bool        `gorm:"default:true" json:"is_regular"`
	BloodGroup       string      `gorm:"size:5" json:"blood_group"`
	Weight           int         `gorm:"default:0" json:"weight"` // Firestore "orderBy"
	IsCR             bool        `gorm:"default:false" json:"is_cr"`
	VerificationCode string      `gorm:"size:20;index" json:"verification_code"` // For profile claiming
	IsClaimed        bool        `gorm:"default:false" json:"is_claimed"`
	ClaimedAt        *time.Time  `json:"claimed_at,omitempty"`
}
