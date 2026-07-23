package domain

import (
	"context"

	"github.com/google/uuid"
)

type MerchantStatus string

const (
	MerchantStatusPending  MerchantStatus = "pending"
	MerchantStatusApproved MerchantStatus = "approved"
	MerchantStatusRejected MerchantStatus = "rejected"
)

// Merchant is a seller in the Campus Marketplace. A student applies via the
// app (status starts "pending"); an admin approves/rejects in the admin
// panel. IsPlatform marks the single synthetic row representing Campus
// Assistant's own in-house products, so every Product always has a
// MerchantID FK with no nullable-merchant special case.
type Merchant struct {
	Base
	UserID       uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	BusinessName string    `gorm:"size:255;not null" json:"business_name"`
	Description  string    `gorm:"type:text" json:"description"`
	LogoURL      string    `gorm:"size:500" json:"logo_url"`
	// BusinessType is a free-text category (e.g. "Food & Beverage",
	// "Electronics") — shown publicly on the merchant's storefront, unlike
	// Phone/Email which are contact details for admin use only (see
	// MerchantHandler.GetMerchantByID/GetPlatformMerchant, which redact
	// Phone/Email from the public response).
	BusinessType    string         `gorm:"size:100" json:"business_type,omitempty"`
	Phone           string         `gorm:"size:20" json:"phone,omitempty"`
	Email           string         `gorm:"size:255" json:"email,omitempty"`
	CommissionRate  float64        `gorm:"default:0" json:"commission_rate"` // percentage points, e.g. 10 = 10%
	Status          MerchantStatus `gorm:"type:varchar(20);default:'pending';index" json:"status"`
	IsPlatform      bool           `gorm:"default:false;index" json:"is_platform"`
	RejectionReason string         `gorm:"type:text" json:"rejection_reason,omitempty"`
}

// MerchantRepository is dedicated (not generic CRUD) because approval/
// rejection are distinct state transitions and platform-lookup/ownership
// checks need custom queries, same reasoning as SkillRepository.
type MerchantRepository interface {
	GetAllMerchants(ctx context.Context, status MerchantStatus) ([]Merchant, error)
	GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error)
	GetMerchantByUserID(ctx context.Context, userID uuid.UUID) (*Merchant, error)
	GetOrCreatePlatformMerchant(ctx context.Context) (*Merchant, error)
	CreateMerchant(ctx context.Context, merchant *Merchant) error
	UpdateMerchant(ctx context.Context, merchant *Merchant) error
	SetMerchantStatus(ctx context.Context, id uuid.UUID, status MerchantStatus, rejectionReason string) error
	DeleteMerchant(ctx context.Context, id uuid.UUID) error
}
