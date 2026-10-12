package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
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

// ActiveCR narrows a CR query to rows that currently grant CR powers: flagged
// current, tied to a batch, and inside their term. It is the one definition of
// "is a CR right now" — class-notice authorization and the is_cr flag sent to
// the app both use it, so the app never offers an action the server refuses.
func ActiveCR(db *gorm.DB) *gorm.DB {
	return db.Where(
		"is_current = true AND batch_id <> ? AND (term_end IS NULL OR term_end > ?)",
		uuid.Nil, time.Now(),
	)
}

// CRsOf narrows a CR query to rows held by userID, whether the row is linked
// to the account directly (user_id) or through the student profile it was
// created for (target_student_id) — imported CR rows only have the latter.
func CRsOf(userID uuid.UUID) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where(
			"(user_id = ? OR target_student_id IN (SELECT id FROM students WHERE user_id = ?))",
			userID, userID,
		)
	}
}
