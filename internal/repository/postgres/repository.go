package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type GormRepository[T any] struct {
	DB *gorm.DB
}

func NewGormRepository[T any](db *gorm.DB) domain.Repository[T] {
	return &GormRepository[T]{DB: db}
}

func (r *GormRepository[T]) Create(ctx context.Context, entity *T) error {
	return r.DB.WithContext(ctx).Create(entity).Error
}

func (r *GormRepository[T]) GetByID(ctx context.Context, id uuid.UUID) (*T, error) {
	var entity T
	// Assumes the entity struct has a field named "ID" or similar mapping.
	// GORM handles this well if the ID is the primary key.
	if err := r.DB.WithContext(ctx).First(&entity, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

func (r *GormRepository[T]) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]T, int64, error) {
	var entities []T
	var count int64

	query := r.DB.WithContext(ctx).Model(new(T))

	// Apply filters
	if len(filter) > 0 {
		query = query.Where(filter)
	}

	// Count total
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	// Fetch with pagination
	if err := query.Limit(limit).Offset(offset).Find(&entities).Error; err != nil {
		return nil, 0, err
	}
	return entities, count, nil
}

func (r *GormRepository[T]) Update(ctx context.Context, entity *T) error {
	return r.DB.WithContext(ctx).Save(entity).Error
}

func (r *GormRepository[T]) Delete(ctx context.Context, id uuid.UUID) error {
	// Hard delete or Soft delete? GORM defaults to soft delete if DeletedAt is present.
	// We want soft delete as per our Base struct.
	return r.DB.WithContext(ctx).Delete(new(T), "id = ?", id).Error
}
