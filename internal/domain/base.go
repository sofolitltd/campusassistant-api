package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Entity interface ensures all models have ID management.
type Entity interface {
	GetID() uuid.UUID
	SetID(id uuid.UUID)
}

// Auditable interface ensures models can track who created/updated them.
type Auditable interface {
	SetCreatedBy(id uuid.UUID)
	SetUpdatedBy(id uuid.UUID)
}

// Base contains common columns for all tables.
type Base struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	// Audit Trail
	CreatedByID uuid.UUID `gorm:"type:uuid;index" json:"created_by_id"`
	UpdatedByID uuid.UUID `gorm:"type:uuid;index" json:"updated_by_id"`
}

func (b *Base) GetID() uuid.UUID {
	return b.ID
}

func (b *Base) SetID(id uuid.UUID) {
	b.ID = id
}

func (b *Base) SetCreatedBy(id uuid.UUID) {
	b.CreatedByID = id
}

func (b *Base) SetUpdatedBy(id uuid.UUID) {
	b.UpdatedByID = id
}

// BeforeCreate ensures UUID is generated.
func (b *Base) BeforeCreate(tx *gorm.DB) (err error) {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	return
}
