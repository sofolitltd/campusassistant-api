package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type feedbackRepository struct {
	db *gorm.DB
}

func NewFeedbackRepository(db *gorm.DB) domain.FeedbackRepository {
	return &feedbackRepository{db: db}
}

func (r *feedbackRepository) Create(ctx context.Context, f *domain.Feedback) error {
	return r.db.WithContext(ctx).Create(f).Error
}

func (r *feedbackRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Feedback, error) {
	var f domain.Feedback
	err := r.db.WithContext(ctx).Preload("User").First(&f, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *feedbackRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Feedback, int64, error) {
	q := r.db.WithContext(ctx).Model(&domain.Feedback{}).Preload("User")
	if v, ok := filter["status"]; ok {
		q = q.Where("status = ?", v)
	}
	if v, ok := filter["category"]; ok {
		q = q.Where("category = ?", v)
	}
	var count int64
	q.Count(&count)
	q = q.Order("created_at desc").Limit(limit).Offset(offset)
	var items []domain.Feedback
	err := q.Find(&items).Error
	return items, count, err
}

func (r *feedbackRepository) Update(ctx context.Context, f *domain.Feedback) error {
	return r.db.WithContext(ctx).Save(f).Error
}

func (r *feedbackRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Feedback{}, "id = ?", id).Error
}

func (r *feedbackRepository) HardDelete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Unscoped().Delete(&domain.Feedback{}, "id = ?", id).Error
}

func (r *feedbackRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Feedback, error) {
	var items []domain.Feedback
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at desc").Find(&items).Error
	return items, err
}
