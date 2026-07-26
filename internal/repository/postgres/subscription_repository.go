package postgres

import (
	"context"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type subscriptionRepository struct {
	db *gorm.DB
}

func NewSubscriptionRepository(db *gorm.DB) domain.SubscriptionRepository {
	return &subscriptionRepository{db: db}
}

func (r *subscriptionRepository) GetPlansByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]domain.SubscriptionPlan, error) {
	var plans []domain.SubscriptionPlan
	
	// Find plans that target this university (either specific department or whole uni)
	err := r.db.WithContext(ctx).
		Distinct("subscription_plans.*").
		Joins("JOIN subscription_targets ON subscription_targets.plan_id = subscription_plans.id").
		Where("subscription_targets.university_id = ? AND (subscription_targets.department_id = ? OR subscription_targets.department_id = ?)", 
			universityID, departmentID, uuid.Nil).
		Order("subscription_plans.index asc").
		Preload("Targets").
		Find(&plans).Error
		
	return plans, err
}

func (r *subscriptionRepository) GetUserSubscription(ctx context.Context, userID uuid.UUID) (*domain.UserSubscription, error) {
	var sub domain.UserSubscription
	err := r.db.WithContext(ctx).
		Preload("User").
		Where("user_id = ?", userID).
		Order("created_at desc").
		First(&sub).Error
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *subscriptionRepository) CreateUserSubscription(ctx context.Context, sub *domain.UserSubscription) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Cache plan data at purchase time so edits don't change historical records
		var plan domain.SubscriptionPlan
		if err := tx.First(&plan, "id = ?", sub.PlanID).Error; err == nil {
			if sub.Price == 0 {
				sub.Price = float64(plan.Price)
			}
			if sub.Plan == "" {
				sub.Plan = plan.Title
			}
		}

		// 1. Create the subscription record
		if err := tx.Create(sub).Error; err != nil {
			return err
		}

		// 2. Update user status and expiry date
		return tx.Model(&domain.User{}).
			Where("id = ?", sub.UserID).
			Updates(map[string]interface{}{
				"is_pro":     true,
				"pro_expiry": sub.EndDate,
			}).Error
	})
}

// Admin Methods
func (r *subscriptionRepository) GetAllPlans(ctx context.Context) ([]domain.SubscriptionPlan, error) {
	var plans []domain.SubscriptionPlan
	err := r.db.WithContext(ctx).Preload("Targets").Order("index asc").Find(&plans).Error
	return plans, err
}

func (r *subscriptionRepository) CreatePlan(ctx context.Context, plan *domain.SubscriptionPlan) error {
	return r.db.WithContext(ctx).Create(plan).Error
}

func (r *subscriptionRepository) UpdatePlan(ctx context.Context, plan *domain.SubscriptionPlan) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete old targets first to handle multi-select updates correctly
		if err := tx.Where("plan_id = ?", plan.ID).Delete(&domain.SubscriptionTarget{}).Error; err != nil {
			return err
		}
		// Save plan (updates fields and inserts new targets)
		return tx.Save(plan).Error
	})
}

func (r *subscriptionRepository) DeletePlan(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.SubscriptionPlan{}, id).Error
}

func (r *subscriptionRepository) GetAllSubscriptions(ctx context.Context, offset, limit int) ([]domain.UserSubscription, int64, float64, error) {
	var subs []domain.UserSubscription
	var count int64
	var totalRevenue float64

	r.db.WithContext(ctx).Model(&domain.UserSubscription{}).Count(&count)
	r.db.WithContext(ctx).Model(&domain.UserSubscription{}).Select("COALESCE(SUM(price), 0)").Scan(&totalRevenue)

	err := r.db.WithContext(ctx).
		Preload("User").
		Order("start_date desc").
		Offset(offset).Limit(limit).
		Find(&subs).Error
	return subs, count, totalRevenue, err
}

func (r *subscriptionRepository) ExpireSubscriptions(ctx context.Context) (int64, error) {
	now := time.Now()
	
	// Find users with expired Pro status
	var expiredUserIDs []uuid.UUID
	err := r.db.WithContext(ctx).Model(&domain.User{}).
		Where("is_pro = ? AND pro_expiry < ?", true, now).
		Pluck("id", &expiredUserIDs).Error
		
	if err != nil || len(expiredUserIDs) == 0 {
		return 0, err
	}

	// Batch update status to false
	result := r.db.WithContext(ctx).Model(&domain.User{}).
		Where("id IN ?", expiredUserIDs).
		Update("is_pro", false)
		
	return result.RowsAffected, result.Error
}
