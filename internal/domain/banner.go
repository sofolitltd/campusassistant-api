package domain

import (
	"time"

	"github.com/google/uuid"
)

// Banner represents a banner advertisement or announcement.
type Banner struct {
	Base
	Title       string         `gorm:"size:255;not null" json:"title"`
	ImageURL    string         `json:"image_url"`
	ClickURL    string         `json:"click_url"`
	Priority    int            `gorm:"default:0" json:"priority"`
	IsActive    bool           `gorm:"default:true" json:"is_active"`
	StartAt     time.Time      `json:"start_at"`
	EndAt       time.Time      `json:"end_at"`
	TargetScope string         `gorm:"size:20;not null" json:"target_scope"` // Global, University, Department
	Targets     []BannerTarget `gorm:"foreignKey:BannerID;constraint:OnDelete:CASCADE" json:"targets,omitempty"`
}

// BannerTarget defines who sees the banner.
type BannerTarget struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	BannerID     *uuid.UUID `gorm:"type:uuid;index;not null" json:"banner_id,omitempty"`
	UniversityID *uuid.UUID `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID `gorm:"type:uuid;index" json:"department_id,omitempty"`
}
