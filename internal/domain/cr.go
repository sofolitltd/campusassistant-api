package domain

import (
	"time"

	"github.com/google/uuid"
)

// CR represents a Class Representative.
type CR struct {
	Base
	UserID       *uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty"`
	User         *User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"user,omitempty"`
	UniversityID uuid.UUID  `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID uuid.UUID  `gorm:"type:uuid;not null;index" json:"department_id"`

	// The student associated with this role
	TargetStudentID *uuid.UUID `gorm:"type:uuid;index" json:"target_student_id,omitempty"`
	TargetStudent   *Student   `gorm:"constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"target_student,omitempty"`

	Name      string     `gorm:"size:100;not null" json:"name"`
	StudentID string     `gorm:"size:50" json:"student_id"`
	Email     string     `gorm:"size:100" json:"email"`
	Phone     string     `gorm:"size:20" json:"phone"`
	BatchID   uuid.UUID  `gorm:"type:uuid;index" json:"batch_id"`
	Batch     string     `gorm:"size:50" json:"batch"`
	TermStart *time.Time `json:"term_start,omitempty"`
	TermEnd   *time.Time `json:"term_end,omitempty"`
	Fb        string     `gorm:"size:255" json:"fb"`
	ImageURL  string     `gorm:"size:500" json:"image_url"`
	IsCurrent bool       `gorm:"default:false" json:"is_current"`
}
