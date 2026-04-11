package domain

import (
	"github.com/google/uuid"
)

// EmergencyContact represents an emergency or administrative contact.
type EmergencyContact struct {
	Base
	Title        string     `gorm:"size:255;not null" json:"title"`
	Designation  string     `gorm:"size:255" json:"designation"`
	Description  string     `gorm:"type:text" json:"description"`
	Phone        string     `gorm:"size:50;not null" json:"phone"`
	Email        string     `gorm:"size:100" json:"email"`
	Category     string     `gorm:"size:100;index" json:"category"`      // e.g., Health, Security, Admin
	Scope        string     `gorm:"size:50;index;not null" json:"scope"` // department, university, national
	UniversityID *uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"`
	IsVerified   bool       `gorm:"default:false" json:"is_verified"`
	LogoURL      string     `gorm:"type:text" json:"logo_url"`
}

func (EmergencyContact) TableName() string {
	return "contacts"
}
