package domain

import (
	"time"

	"github.com/google/uuid"
)

// ClassNotice is a short-lived announcement for one batch of a department
// ("tomorrow's class is cancelled", an exam routine photo). Class
// Representatives post them for their own batch; admins can post for any
// batch (BatchID nil = the whole department) and moderate everything.
type ClassNotice struct {
	Base
	UniversityID uuid.UUID  `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID uuid.UUID  `gorm:"type:uuid;not null;index" json:"department_id"`
	BatchID      *uuid.UUID `gorm:"type:uuid;index" json:"batch_id,omitempty"`
	Title        string     `gorm:"size:200;not null" json:"title"`
	Message      string     `gorm:"type:text" json:"message"`
	FileURL      string     `gorm:"size:500" json:"file_url"`
	FileType     string     `gorm:"size:10" json:"file_type"` // "image" | "pdf" | "" (text only)
	ExpiresAt    *time.Time `gorm:"index" json:"expires_at,omitempty"`
	PostedByName string     `gorm:"size:100" json:"posted_by_name"`
}
