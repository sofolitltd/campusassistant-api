package domain

import "github.com/google/uuid"

// Transport represents transport information (e.g. Bus Route).
type Transport struct {
	Base
	UniversityID  uuid.UUID `gorm:"type:uuid;not null;index" json:"university_id"`
	RouteName     string    `gorm:"size:100;not null" json:"route_name"`
	BusNumber     string    `json:"bus_number"`
	Schedule      string    `gorm:"type:text" json:"schedule"` // e.g. JSON or text description
	DriverContact string    `json:"driver_contact"`
}
