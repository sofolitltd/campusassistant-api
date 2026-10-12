package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type studentVerificationRepository struct {
	db *gorm.DB
}

func NewStudentVerificationRepository(db *gorm.DB) domain.StudentVerificationRepository {
	return &studentVerificationRepository{db: db}
}

func (r *studentVerificationRepository) withRelations(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Preload("User").
		Preload("University").
		Preload("Department").
		Preload("Batch").
		Preload("Session")
}

func (r *studentVerificationRepository) Create(ctx context.Context, v *domain.StudentVerificationRequest) error {
	return r.db.WithContext(ctx).Create(v).Error
}

func (r *studentVerificationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.StudentVerificationRequest, error) {
	var v domain.StudentVerificationRequest
	if err := r.withRelations(ctx).First(&v, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *studentVerificationRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.StudentVerificationRequest, int64, error) {
	q := r.db.WithContext(ctx).Model(&domain.StudentVerificationRequest{})
	for _, col := range []string{"status", "university_id", "department_id"} {
		if v, ok := filter[col]; ok {
			q = q.Where(col+" = ?", v)
		}
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var items []domain.StudentVerificationRequest
	err := q.Preload("User").
		Preload("University").
		Preload("Department").
		Preload("Batch").
		Preload("Session").
		Order("created_at desc").Limit(limit).Offset(offset).
		Find(&items).Error
	return items, count, err
}

func (r *studentVerificationRepository) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*domain.StudentVerificationRequest, error) {
	var v domain.StudentVerificationRequest
	err := r.withRelations(ctx).Where("user_id = ?", userID).Order("created_at desc").First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}
