package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SubscriptionPlan represents a tier of subscription available for a specific location.
type SubscriptionPlan struct {
	Base
	Title        string    `gorm:"size:100;not null" json:"title"`
	Price        int       `gorm:"not null" json:"price"`
	Discount     int       `gorm:"default:0" json:"discount"`
	Index        int       `gorm:"default:0" json:"index"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}

// ProFeature represents a feature unlocked by Pro subscription.
type ProFeature struct {
	Base
	Title        string    `gorm:"size:255;not null" json:"title"`
	Index        int       `gorm:"default:0" json:"index"`
	UniversityID uuid.UUID `gorm:"type:uuid;index" json:"university_id"`
	DepartmentID uuid.UUID `gorm:"type:uuid;index" json:"department_id"`
}

// UserSubscription links a user to their active subscription plan.
type UserSubscription struct {
	Base
	UserID    uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Plan      string    `gorm:"size:50;not null" json:"plan"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
}

// SubscriptionRepository defines database operations for subscriptions.
type SubscriptionRepository interface {
	GetPlansByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]SubscriptionPlan, error)
	GetFeaturesByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]ProFeature, error)
	GetUserSubscription(ctx context.Context, userID uuid.UUID) (*UserSubscription, error)
	CreateUserSubscription(ctx context.Context, sub *UserSubscription) error
}
