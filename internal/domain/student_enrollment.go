package domain

import "github.com/google/uuid"

// StudentEnrollment is a program a student was in before their current one
// (e.g. the Honors batch they finished before Masters). The *current*
// enrollment is not stored here — it stays on Student itself (BatchID,
// SessionID, StudentID roll, IsCR, IsRegular), so everything that reads those
// columns keeps working. Promote copies those columns into a row here, then
// points Student at the new batch.
type StudentEnrollment struct {
	Base
	StudentID    uuid.UUID `gorm:"type:uuid;not null;index" json:"student_id"`
	Student      *Student  `json:"student,omitempty"`
	BatchID      uuid.UUID `gorm:"type:uuid;not null;index" json:"batch_id"`
	Batch        *Batch    `json:"batch,omitempty"`
	SessionID    uuid.UUID `gorm:"type:uuid;not null" json:"session_id"`
	Session      *Session  `json:"session,omitempty"`
	DepartmentID uuid.UUID `gorm:"type:uuid;not null;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	Program      string    `gorm:"size:20;not null" json:"program"` // the batch's program at the time
	Roll         string    `gorm:"size:50" json:"roll"`             // Student.StudentID while in this program
	IsCR         bool      `gorm:"default:false" json:"is_cr"`
	IsRegular    bool      `gorm:"default:true" json:"is_regular"`
	Status       string    `gorm:"size:20;not null;default:'completed'" json:"status"` // EnrollmentStatus*
}

const (
	EnrollmentStatusCompleted = "completed"
)
