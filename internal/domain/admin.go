package domain

import "github.com/google/uuid"

type Admin struct {
	Base
	Email        string `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string `gorm:"size:255" json:"-"`
	Name         string `gorm:"size:100" json:"name"`
	Role         string `gorm:"size:20;default:'super_admin'" json:"role"`
	IsActive     bool   `gorm:"default:true" json:"is_active"`
	CreatedBy    *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
}
