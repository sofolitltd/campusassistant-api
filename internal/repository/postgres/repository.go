package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if err := r.DB.WithContext(ctx).Preload(clause.Associations).First(&entity, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &entity, nil
}

func (r *GormRepository[T]) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]T, int64, error) {
	var entities []T
	var count int64

	// Use a session to avoid polluting the main DB instance
	db := r.DB.WithContext(ctx).Model(new(T))

	// Apply filters
	shouldPreload := false
	for key, value := range filter {
		if key == "search" {
			searchVal := "%" + value.(string) + "%"
			db = db.Where("name ILIKE ? OR title ILIKE ? OR designation ILIKE ? OR email ILIKE ? OR phone ILIKE ? OR student_id ILIKE ?", searchVal, searchVal, searchVal, searchVal, searchVal, searchVal)
		} else if key == "preload" {
			if b, ok := value.(bool); ok && b {
				shouldPreload = true
			}
		} else {
			db = db.Where(key+" = ?", value)
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	if shouldPreload {
		db = db.Preload(clause.Associations)
	}

	err := db.Limit(limit).Offset(offset).Find(&entities).Error
	if err != nil {
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
