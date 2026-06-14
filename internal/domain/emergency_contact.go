package domain

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
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
	TargetScope  string          `gorm:"size:50;index;not null" json:"target_scope"` // National, University, Department
	Targets      []ContactTarget `gorm:"foreignKey:ContactID;constraint:OnDelete:CASCADE" json:"targets,omitempty"`
	IsVerified   bool            `gorm:"default:false" json:"is_verified"`
	LogoURL      string          `gorm:"type:text" json:"logo_url"`
}

// ContactTarget defines who sees the emergency contact.
type ContactTarget struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	ContactID    *uuid.UUID `gorm:"type:uuid;index;not null" json:"contact_id,omitempty"`
	UniversityID *uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"`
}

func (EmergencyContact) TableName() string {
	return "contacts"
}

// BeforeSave hook clears old targets so GORM can seamlessly replace them with the new payload
func (e *EmergencyContact) BeforeSave(tx *gorm.DB) error {
	if e.ID != uuid.Nil {
		tx.Where("contact_id = ?", e.ID).Delete(&ContactTarget{})
	}
	return nil
}
