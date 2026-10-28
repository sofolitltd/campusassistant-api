package domain

import (
	"time"

	"github.com/google/uuid"
)

// ExamNotice is a scheduled exam for a department (optionally narrowed to one
// batch) — shown to students as an "Exams" list with a countdown, and
// overlaid on the Class Routine calendar. Admin-only writes; any signed-in
// student can read.
type ExamNotice struct {
	Base
	UniversityID uuid.UUID  `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID uuid.UUID  `gorm:"type:uuid;not null;index" json:"department_id"`
	BatchID      *uuid.UUID `gorm:"type:uuid;index" json:"batch_id,omitempty"`
	CourseName   string     `gorm:"size:200;not null" json:"course_name"`
	// CourseCode links to Course.CourseCode (not a FK — Course is looked up by
	// code in the Study feature, so storing the code instead of an ID lets the
	// student app deep-link straight into /study/courses/:courseCode).
	CourseCode   string     `gorm:"size:50;index" json:"course_code"`
	ExamType     string     `gorm:"size:20;not null" json:"exam_type"` // "midterm" | "final" | "quiz" | "retake" | "improvement"
	ExamDate     time.Time  `gorm:"not null;index" json:"exam_date"`
	StartTime    string     `gorm:"size:10" json:"start_time"` // "HH:MM"
	Room         string     `gorm:"size:100" json:"room"`
	SeatPlanURL  string     `gorm:"size:500" json:"seat_plan_url"`
	SyllabusURL  string     `gorm:"size:500" json:"syllabus_url"`
	Note         string     `gorm:"type:text" json:"note"`

	// ReminderSentAt marks the one-day-before push as delivered, so
	// ExamReminderScheduler's hourly sweep never sends it twice.
	ReminderSentAt *time.Time `json:"reminder_sent_at,omitempty"`
}
