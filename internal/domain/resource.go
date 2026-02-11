package domain

import "github.com/google/uuid"

// ResourceType defines the type of content.
type ResourceType string

const (
	TypeNote     ResourceType = "note"
	TypeQuestion ResourceType = "question"
	TypeSyllabus ResourceType = "syllabus"
	TypeBook     ResourceType = "book"
)

// Resource represents academic content (Book, Question, Note, Syllabus).
// We use a single table with a Type discriminator for simplicity, or separate tables if they diverge significantly.
// User requested separate endpoints /books, /questions, etc.
// But they share many fields (Title, URL, DepartmentID).
// Let's use separate structs for clarity but maybe share a base if complex. For now, simple separate structs.

type Book struct {
	Base
	Title        string    `gorm:"size:255;not null" json:"title"`
	Author       string    `gorm:"size:255" json:"author"`
	Edition      string    `json:"edition"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DownloadURL  string    `json:"download_url"`
	PhysicalLoc  string    `json:"physical_location"` // If library book
}

type Question struct {
	Base
	Subject      string    `gorm:"size:255;not null" json:"subject"`
	Year         int       `json:"year"`
	Semester     string    `json:"semester"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DownloadURL  string    `json:"download_url"`
}

type Note struct {
	Base
	Title        string    `gorm:"size:255;not null" json:"title"`
	Subject      string    `json:"subject"`
	Topic        string    `json:"topic"`
	TeacherID    uuid.UUID `gorm:"type:uuid;index" json:"teacher_id,omitempty"` // Uploaded by
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DownloadURL  string    `json:"download_url"`
}

type Syllabus struct {
	Base
	SessionID    uuid.UUID `gorm:"type:uuid;index" json:"session_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DownloadURL  string    `json:"download_url"`
}

// Transport represents transport information (e.g. Bus Route).
type Transport struct {
	Base
	UniversityID  uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	RouteName     string    `gorm:"size:100;not null" json:"route_name"`
	BusNumber     string    `json:"bus_number"`
	Schedule      string    `gorm:"type:text" json:"schedule"` // e.g. JSON or text description
	DriverContact string    `json:"driver_contact"`
}
