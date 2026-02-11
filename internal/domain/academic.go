package domain

import "github.com/google/uuid"

// University represents a university entity.
type University struct {
	Base
	Name        string       `gorm:"size:255;not null;unique" json:"name"`
	Acronym     string       `gorm:"size:20;index" json:"acronym"`
	Address     string       `gorm:"type:text" json:"address"`
	Website     string       `gorm:"size:255" json:"website"`
	Departments []Department `gorm:"foreignKey:UniversityID" json:"departments,omitempty"`
	Sessions    []Session    `gorm:"foreignKey:UniversityID" json:"sessions,omitempty"`
	LogoURL     string       `json:"logo_url"`
}

// Department represents a department within a university.
type Department struct {
	Base
	Name         string      `gorm:"size:255;not null" json:"name"`
	Code         string      `gorm:"size:20" json:"code"` // e.g., CSE, EEE
	UniversityID uuid.UUID   `gorm:"type:uuid;not null;index" json:"university_id"`
	University   *University `json:"university,omitempty"`
	Batches      []Batch     `gorm:"foreignKey:DepartmentID" json:"batches,omitempty"`
	Semesters    int         `json:"semesters"` // Total semesters (e.g., 8)
}

// Session represents an academic session (e.g., 2023-2024).
type Session struct {
	Base
	Name         string    `gorm:"size:50;not null" json:"name"` // 2023-2024
	UniversityID uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	IsActive     bool      `gorm:"default:true" json:"is_active"`
}

// Batch represents a specific batch of students (e.g., "CSE - 25th Batch").
type Batch struct {
	Base
	Name         string      `gorm:"size:100;not null" json:"name"`
	DepartmentID uuid.UUID   `gorm:"type:uuid;not null;index" json:"department_id"`
	Department   *Department `json:"department,omitempty"`
	SessionID    uuid.UUID   `gorm:"type:uuid;not null;index" json:"session_id"`
	Session      *Session    `json:"session,omitempty"`
	Students     []Student   `gorm:"foreignKey:BatchID" json:"students,omitempty"`
}
