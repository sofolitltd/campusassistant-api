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

// UserSubscription links a user to their active subscription plan.
type UserSubscription struct {
	Base
	UserID    uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	User      User      `gorm:"foreignKey:UserID" json:"user"`
	PlanID    uuid.UUID `gorm:"type:uuid;not null" json:"plan_id"`
	Plan      string    `gorm:"size:50;not null" json:"plan"` // Cached title
	Price     float64   `json:"price"`
	StartDate time.Time `json:"start_date"`
	// EndDate is nil for a Lifetime plan — mirrors User.ProExpiry, and the
	// expiry worker's `pro_expiry < now` query already treats NULL as
	// "never expires" per normal SQL semantics.
	EndDate *time.Time `json:"end_date"`
}

// SubscriptionRepository defines database operations for subscriptions.
type SubscriptionRepository interface {
	GetPlansByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]SubscriptionPlan, error)
	GetUserSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error)
	CreateUserSubscription(ctx context.Context, sub *UserSubscription) error

	// Admin Methods
	GetAllPlans(ctx context.Context) ([]SubscriptionPlan, error)
	CreatePlan(ctx context.Context, plan *SubscriptionPlan) error
	UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error
	DeletePlan(ctx context.Context, id uuid.UUID) error

	GetAllSubscriptions(ctx context.Context, offset, limit int) ([]UserSubscription, int64, float64, error)
	ExpireSubscriptions(ctx context.Context) (int64, error)
}
