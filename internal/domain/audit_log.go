package domain

import (
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	Base
	UserID      uuid.UUID `gorm:"type:uuid;index" json:"user_id"`
	Action      string    `gorm:"size:50" json:"action"`      // e.g. "CREATE", "UPDATE", "DELETE", "LOGIN"
	EntityName  string    `gorm:"size:50" json:"entity_name"` // e.g. "Student", "Book"
	EntityID    uuid.UUID `gorm:"type:uuid" json:"entity_id"`
	Description string    `gorm:"type:text" json:"description"`
	IPAddress   string    `gorm:"size:45" json:"ip_address"`
	UserAgent   string    `gorm:"type:text" json:"user_agent"`
	Timestamp   time.Time `json:"timestamp"`
}
