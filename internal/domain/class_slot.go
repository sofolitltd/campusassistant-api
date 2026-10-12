package domain

import "github.com/google/uuid"

// ClassSlot is one weekly recurring class period for a batch. CRs enter
// these once at the start of a semester from the printed routine; admins
// can manage them too. DayOfWeek follows time.Weekday (Sunday=0..Saturday=6).
type ClassSlot struct {
	Base
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;not null;index" json:"department_id"`
	BatchID      uuid.UUID `gorm:"type:uuid;not null;index" json:"batch_id"`
	DayOfWeek    int       `gorm:"not null;index" json:"day_of_week"` // 0=Sunday..6=Saturday
	CourseName   string    `gorm:"size:200;not null" json:"course_name"`
	Teacher      string    `gorm:"size:150" json:"teacher"`
	Room         string    `gorm:"size:100" json:"room"`
	StartTime    string    `gorm:"size:5;not null" json:"start_time"` // "HH:MM", 24h
	EndTime      string    `gorm:"size:5;not null" json:"end_time"`
}
