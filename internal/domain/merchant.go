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
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	// User is populated manually (not a GORM-managed association — the
	// platform merchant's UserID is a zero UUID with no matching row, so a
	// real FK/auto-preload would fail) only for admin-facing reads, so an
	// admin can cross-check the applicant's real account identity against
	// their uploaded ID proofs.
	User         *User  `gorm:"-" json:"user,omitempty"`
	BusinessName string `gorm:"size:255;not null" json:"business_name"`
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
	Website         string         `gorm:"size:500" json:"website,omitempty"`
	SocialMediaLink string         `gorm:"size:500" json:"social_media_link,omitempty"`
	// StudentIDProofURL/NIDProofURL are uploaded by the applicant so an admin
	// can verify identity before approving — same admin-only visibility as
	// Phone/Email (redacted from the public storefront lookup).
	StudentIDProofURL string `gorm:"size:500" json:"student_id_proof_url,omitempty"`
	NIDProofURL       string `gorm:"size:500" json:"nid_proof_url,omitempty"`
	// PayoutMethod/PayoutAccount is where commission-adjusted revenue is
	// actually sent — distinct from Phone/Email, which are just contact
	// details. Admin-only visibility, same as Phone/Email.
	PayoutMethod  string `gorm:"size:20" json:"payout_method,omitempty"`  // e.g. "bkash", "nagad", "bank"
	PayoutAccount string `gorm:"size:255" json:"payout_account,omitempty"` // account/wallet number
}

// MerchantRepository is dedicated (not generic CRUD) because approval/
// rejection are distinct state transitions and platform-lookup/ownership
// checks need custom queries, same reasoning as SkillRepository.
type MerchantRepository interface {
	GetAllMerchants(ctx context.Context, status MerchantStatus) ([]Merchant, error)
	GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error)
	// GetMerchantWithUserByID is GetMerchantByID plus a preloaded owning
	// User, for admin/read-only display only (e.g. cross-checking ID proofs
	// against the applicant's account). Deliberately separate from
	// GetMerchantByID, which is also used ahead of UpdateMerchant's Save —
	// GORM auto-saves populated associations on Save, so preloading User
	// there risks silently rewriting the User row.
	GetMerchantWithUserByID(ctx context.Context, id uuid.UUID) (*Merchant, error)
	GetMerchantByUserID(ctx context.Context, userID uuid.UUID) (*Merchant, error)
	// GetMerchantsByUserID returns every merchant/business owned by userID —
	// a single student may run more than one storefront (e.g. a food stall
	// and a tutoring service), unlike GetMerchantByUserID which only ever
	// returns the first match.
	GetMerchantsByUserID(ctx context.Context, userID uuid.UUID) ([]Merchant, error)
	GetOrCreatePlatformMerchant(ctx context.Context) (*Merchant, error)
	CreateMerchant(ctx context.Context, merchant *Merchant) error
	UpdateMerchant(ctx context.Context, merchant *Merchant) error
	SetMerchantStatus(ctx context.Context, id uuid.UUID, status MerchantStatus, rejectionReason string) error
	DeleteMerchant(ctx context.Context, id uuid.UUID) error
	// ExistsByBusinessName does a case-insensitive check, excluding a given
	// merchant id (for edits) — used to reject duplicate business names.
	ExistsByBusinessName(ctx context.Context, businessName string, excludeID uuid.UUID) (bool, error)
}
