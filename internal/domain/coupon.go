package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type CouponDiscountType string

const (
	CouponPercentage CouponDiscountType = "percentage"
	CouponFixed      CouponDiscountType = "fixed"
)

// CouponCode represents a redeemable promo/discount code.
type CouponCode struct {
	Base
	Code          string             `gorm:"uniqueIndex;size:50;not null" json:"code"`
	DiscountType  CouponDiscountType `gorm:"type:varchar(20);not null;default:'percentage'" json:"discount_type"`
	DiscountValue int                `gorm:"not null" json:"discount_value"` // percentage (e.g. 50) or amount in BDT (e.g. 500)
	MaxUses       int                `gorm:"not null;default:1" json:"max_uses"`
	UsedCount     int                `gorm:"not null;default:0" json:"used_count"`
	PlanID        *uuid.UUID         `gorm:"type:uuid;index" json:"plan_id,omitempty"`     // nil = any plan
	MinAmount     int                `gorm:"not null;default:0" json:"min_amount"`          // minimum plan price for this coupon to apply
	ExpiresAt     *time.Time         `json:"expires_at,omitempty"`                          // nil = never expires
	IsActive      bool               `gorm:"not null;default:true" json:"is_active"`
}

// CouponRepository defines database operations for coupon codes.
type CouponRepository interface {
	GetByCode(ctx context.Context, code string) (*CouponCode, error)
	GetByID(ctx context.Context, id uuid.UUID) (*CouponCode, error)
	Create(ctx context.Context, coupon *CouponCode) error
	Update(ctx context.Context, coupon *CouponCode) error
	GetAll(ctx context.Context) ([]CouponCode, error)
}
