package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SubscriptionPlan represents a tier of subscription available for specific locations.
type SubscriptionPlan struct {
	Base
	Title        string `gorm:"size:100;not null" json:"title"`
	Price        int    `gorm:"not null" json:"price"`
	Discount     int    `gorm:"default:0" json:"discount"`
	DurationDays int    `gorm:"default:30;not null" json:"duration_days"` // e.g. 30 for 1 month, 365 for 1 year
	// IsLifetime plans never expire — a granted UserSubscription gets a nil
	// EndDate/ProExpiry instead of one computed from DurationDays.
	IsLifetime bool                 `gorm:"default:false" json:"is_lifetime"`
	Index      int                  `gorm:"default:0" json:"index"`
	Targets    []SubscriptionTarget `gorm:"foreignKey:PlanID;constraint:OnDelete:CASCADE" json:"targets"`
}

// SubscriptionTarget links a plan to specific universities or departments.
type SubscriptionTarget struct {
	Base
	PlanID       uuid.UUID `gorm:"type:uuid;not null;index" json:"plan_id"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}

// GraceDays is how long access continues past EndDate before it's actually
// cut off — a window for the user to renew without losing access outright.
const GraceDays = 7

// UserSubscription links a user to their active subscription plan.
type UserSubscription struct {
	Base
	UserID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	User           User       `gorm:"foreignKey:UserID" json:"user"`
	PlanID         uuid.UUID  `gorm:"type:uuid;not null" json:"plan_id"`
	Plan           string     `gorm:"size:50;not null" json:"plan"` // Cached title
	Price          float64    `json:"price"`
	StartDate      time.Time  `json:"start_date"`
	GrantedBy      *uuid.UUID `gorm:"type:uuid;index" json:"granted_by,omitempty"`            // admin ID who granted this, nil for self-purchased
	GrantedByAdmin *Admin     `gorm:"foreignKey:GrantedBy" json:"granted_by_admin,omitempty"` // for display — resolves GrantedBy to a name
	GrantReason    string     `gorm:"size:30;not null;default:'payment'" json:"grant_reason"` // "payment", "admin_grant", "coupon"
	// Note is the admin's free-text reason for an admin_grant — e.g. "friend,
	// helping test the app". Empty for payment/coupon subscriptions.
	Note string `gorm:"size:500" json:"note,omitempty"`
	// EndDate is nil for a Lifetime plan — the true, paid-for period end.
	// Renewals always stack off this, never off GraceEndsAt.
	EndDate *time.Time `json:"end_date"`
	// GraceEndsAt is EndDate + GraceDays — the real access cutoff that
	// User.ProExpiry mirrors. nil whenever EndDate is nil (lifetime).
	GraceEndsAt *time.Time `json:"grace_ends_at"`
	// RefundedAt is set when the payment behind this subscription was
	// refunded; the subscription no longer counts toward Pro status.
	RefundedAt *time.Time `json:"refunded_at,omitempty"`
}

// ComputeStatus derives this row's lifecycle state from its dates, the same
// way User.ComputeSubscriptionStatus derives "pro"/"basic" — never stored.
func (s *UserSubscription) ComputeStatus(now time.Time) string {
	switch {
	case s.RefundedAt != nil:
		return "refunded"
	case s.EndDate == nil:
		return "active" // lifetime
	case now.Before(*s.EndDate):
		return "active"
	case s.GraceEndsAt != nil && now.Before(*s.GraceEndsAt):
		return "grace"
	default:
		return "expired"
	}
}

// SubscriptionRepository defines database operations for subscriptions.
type SubscriptionRepository interface {
	GetPlansByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]SubscriptionPlan, error)
	GetUserSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error)
	// GetUserSubscriptionHistory returns every subscription row for a user,
	// newest first — the full purchase/grant history, unlike
	// GetUserSubscription which only returns the single latest one.
	GetUserSubscriptionHistory(ctx context.Context, userID uuid.UUID) ([]UserSubscription, error)
	CreateUserSubscription(ctx context.Context, sub *UserSubscription) error

	// Admin Methods
	GetAllPlans(ctx context.Context) ([]SubscriptionPlan, error)
	CreatePlan(ctx context.Context, plan *SubscriptionPlan) error
	UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error
	DeletePlan(ctx context.Context, id uuid.UUID) error

	GetAllSubscriptions(ctx context.Context, offset, limit int) ([]UserSubscription, int64, float64, error)
	// GetGrantedSubscriptions returns every admin_grant row (free Pro given
	// out by an admin), newest first, with the user and granting admin
	// resolved for display — the "who's getting free Pro" shortlist.
	GetGrantedSubscriptions(ctx context.Context, offset, limit int) ([]UserSubscription, int64, error)
	ExpireSubscriptions(ctx context.Context) (int64, error)

	// AdminSetExpiry extends or shortens a user's current subscription by
	// overwriting its EndDate directly (nil = lifetime/never expires), and
	// keeps User.IsPro/ProExpiry in sync with it. Support escalations when
	// a user's access should last longer or shorter than their plan's
	// default duration gives them.
	AdminSetExpiry(ctx context.Context, userID uuid.UUID, endDate *time.Time) (*UserSubscription, error)
}
