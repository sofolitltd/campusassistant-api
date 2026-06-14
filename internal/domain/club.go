package domain

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Club represents a student club or organization within a university.
type Club struct {
	Base
	Name         string         `gorm:"size:255;not null" json:"name"`
	Description  string         `gorm:"type:text" json:"description"`
	ClubType     string         `gorm:"size:50;not null;index" json:"club_type"`
	UniversityID uuid.UUID      `gorm:"type:uuid;not null;index" json:"university_id"`
	DepartmentID *uuid.UUID     `gorm:"type:uuid;index" json:"department_id,omitempty"`
	LogoURL      *string        `gorm:"size:500" json:"logo_url,omitempty"`
	BannerURL    *string        `gorm:"size:500" json:"banner_url,omitempty"`
	FoundedYear  *int           `json:"founded_year,omitempty"`
	IsActive     bool           `gorm:"default:true" json:"is_active"`
	SocialLinks  datatypes.JSON `gorm:"type:jsonb" json:"social_links,omitempty"`
	ContactEmail *string        `gorm:"size:255" json:"contact_email,omitempty"`
	ContactPhone *string        `gorm:"size:50" json:"contact_phone,omitempty"`
}
