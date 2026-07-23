package domain

import (
	"context"

	"github.com/google/uuid"
)

// Address is a saved shipping address for a user. It is snapshotted into an
// Order at checkout time (not a live FK), so later address edits never mutate
// existing order history.
type Address struct {
	Base
	UserID        uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Label         string    `gorm:"size:100;not null" json:"label"`         // e.g. "Home", "Dorm", "Department"
	RecipientName string    `gorm:"size:255;not null" json:"recipient_name"`
	Phone         string    `gorm:"size:20;not null" json:"phone"`
	AddressLine   string    `gorm:"size:500;not null" json:"address_line"`
	City          string    `gorm:"size:100;not null" json:"city"`
	IsDefault     bool      `gorm:"default:false" json:"is_default"`
}

// AddressRepository handles CRUD + a transactional SetDefault that unsets all
// other addresses for the user before marking the target one default.
type AddressRepository interface {
	GetByUser(ctx context.Context, userID uuid.UUID) ([]Address, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Address, error)
	Create(ctx context.Context, address *Address) error
	Update(ctx context.Context, address *Address) error
	Delete(ctx context.Context, id uuid.UUID) error
	SetDefault(ctx context.Context, userID, addressID uuid.UUID) error
}
