package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type AuditLog struct {
	Base
	ActorType    string         `gorm:"size:20;index" json:"actor_type"`
	ActorID      uuid.UUID      `gorm:"type:uuid;index" json:"actor_id"`
	ActorLabel   string         `gorm:"size:255" json:"actor_label"`
	ViaRole      string         `gorm:"size:50" json:"via_role"`
	Action       string         `gorm:"size:100;index" json:"action"`
	EntityType   string         `gorm:"size:100;index" json:"entity_type"`
	EntityID     uuid.UUID      `gorm:"type:uuid;index" json:"entity_id"`
	UniversityID *uuid.UUID     `gorm:"type:uuid;index" json:"university_id,omitempty"`
	DepartmentID *uuid.UUID     `gorm:"type:uuid;index" json:"department_id,omitempty"`
	BatchID      *uuid.UUID     `gorm:"type:uuid;index" json:"batch_id,omitempty"`
	Changes      datatypes.JSON `gorm:"type:jsonb" json:"changes,omitempty"`
	Reason       string         `gorm:"type:text" json:"reason,omitempty"`
	IPAddress    string         `gorm:"size:45" json:"ip_address"`
	UserAgent    string         `gorm:"type:text" json:"user_agent"`
	Timestamp    time.Time      `gorm:"index" json:"timestamp"`

	// Legacy columns remain during the migration so existing installations
	// can add the new audit shape without dropping historical data.
	UserID      uuid.UUID `gorm:"type:uuid;index" json:"user_id,omitempty"`
	EntityName  string    `gorm:"size:50" json:"entity_name,omitempty"`
	Description string    `gorm:"type:text" json:"description,omitempty"`
}
