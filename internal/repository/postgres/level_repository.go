package postgres

import (
	"campusassistant-api/internal/domain"
	"context"

	"gorm.io/gorm"
)

type levelRepository struct {
	domain.Repository[domain.Level]
	db *gorm.DB
}

func NewLevelRepository(db *gorm.DB) domain.Repository[domain.Level] {
	return &levelRepository{
		Repository: NewGormRepository[domain.Level](db),
		db:         db,
	}
}

func (r *levelRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Level, int64, error) {
	var entities []domain.Level
	var count int64

	db := r.db.WithContext(ctx).Model(&domain.Level{})

	// Handle Batch Filtering
	batchID, hasBatchID := filter["batch_id"]
	batchName, hasBatchName := filter["batch"]

	// Handle Batch Filtering via subquery for cleaner DISTINCT handling in COUNT
	if hasBatchID || hasBatchName {
		sub := r.db.Table("level_batches lb").
			Select("lb.level_id").
			Joins("JOIN batches b ON b.id = lb.batch_id")

		if hasBatchID && batchID != "" {
			sub = sub.Where("b.id = ?", batchID)
			delete(filter, "batch_id")
		}
		if hasBatchName && batchName != "" {
			sub = sub.Where("b.name = ?", batchName)
			delete(filter, "batch")
		}
		db = db.Where("levels.id IN (?)", sub)
	}

	// Apply other filters
	for key, value := range filter {
		if key == "search" {
			searchVal := "%" + value.(string) + "%"
			db = db.Where("levels.name ILIKE ?", searchVal)
		} else {
			db = db.Where("levels."+key+" = ?", value)
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	err := db.Order("\"order\" desc").Preload("Batches").Limit(limit).Offset(offset).Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}
