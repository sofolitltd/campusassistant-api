package postgres

import (
	"context"

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
	err := r.db.WithContext(ctx).
		Where("university_id = ? AND department_id = ?", universityID, departmentID).
		Order("index asc").
		Find(&plans).Error
	return plans, err
}

func (r *subscriptionRepository) GetFeaturesByLocation(ctx context.Context, universityID, departmentID uuid.UUID) ([]domain.ProFeature, error) {
	var features []domain.ProFeature
	err := r.db.WithContext(ctx).
		Where("university_id = ? AND department_id = ?", universityID, departmentID).
		Order("index asc").
		Find(&features).Error
	return features, err
}

func (r *subscriptionRepository) GetUserSubscription(ctx context.Context, userID uuid.UUID) (*domain.UserSubscription, error) {
	var sub domain.UserSubscription
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		First(&sub).Error
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *subscriptionRepository) CreateUserSubscription(ctx context.Context, sub *domain.UserSubscription) error {
	return r.db.WithContext(ctx).Create(sub).Error
}
