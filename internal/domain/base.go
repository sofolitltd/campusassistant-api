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

// Base contains common columns for all tables.
type Base struct {
	ID        uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (b *Base) GetID() uuid.UUID {
	return b.ID
}

func (b *Base) SetID(id uuid.UUID) {
	b.ID = id
}

// BeforeCreate ensures UUID is generated.
func (b *Base) BeforeCreate(tx *gorm.DB) (err error) {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	return
}
