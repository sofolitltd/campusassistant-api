package domain

import (
	"gorm.io/datatypes"
)

// University represents a university entity.
type University struct {
	Base
	Name             string          `gorm:"size:255;not null;unique" json:"name"`
	Acronym          string          `gorm:"size:20;not null;unique;index" json:"acronym"` // e.g., DU, BUET
	Slug             string          `gorm:"size:255;not null;unique;index" json:"slug"`   // e.g., university-of-dhaka
	EstablishedYear  string          `gorm:"type:text" json:"established_year"`
	TotalDepartments string          `gorm:"type:text" json:"total_departments"`
	TotalFaculties   string          `gorm:"type:text" json:"total_faculties"`
	TotalHalls       string          `gorm:"type:text" json:"total_halls"`
	CampusArea       string          `gorm:"type:text" json:"campus_area"`        // e.g., "600 Acres"
	About            string          `gorm:"type:text" json:"about"`              // HTML/Markdown content
	Address          string          `gorm:"type:text" json:"address"`            // Physical address
	Latitude         float64         `json:"latitude"`                            // For map pin
	Longitude        float64         `json:"longitude"`                           // For map pin
	Gallery          *datatypes.JSON `gorm:"type:jsonb" json:"gallery,omitempty"` // Stores JSON array of {order, url}
	WebsiteURL       string          `gorm:"size:255" json:"website_url"`
	LogoURL          string          `json:"logo_url"`
	Departments      []Department    `gorm:"foreignKey:UniversityID" json:"departments,omitempty"`
	Sessions         []Session       `gorm:"foreignKey:UniversityID" json:"sessions,omitempty"`
}
