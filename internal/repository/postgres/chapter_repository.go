package postgres

import (
	"campusassistant-api/internal/domain"
	"context"

	"gorm.io/gorm"
)

type chapterRepository struct {
	domain.Repository[domain.Chapter]
	db *gorm.DB
}

func NewChapterRepository(db *gorm.DB) domain.Repository[domain.Chapter] {
	return &chapterRepository{
		Repository: NewGormRepository[domain.Chapter](db),
		db:         db,
	}
}

func (r *chapterRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Chapter, int64, error) {
	var entities []domain.Chapter
	var count int64

	db := r.db.WithContext(ctx).Model(&domain.Chapter{})

	// Handle Batch Filtering
	batchID, hasBatchID := filter["batch_id"]
	batchName, hasBatchName := filter["batch"]

	// Handle Batch Filtering via subquery for cleaner DISTINCT handling in COUNT
	if hasBatchID || hasBatchName {
		sub := r.db.Table("chapter_batches cb").
			Select("cb.chapter_id").
			Joins("JOIN batches b ON b.id = cb.batch_id")

		if hasBatchID && batchID != "" {
			sub = sub.Where("b.id = ?", batchID)
			delete(filter, "batch_id")
		}
		if hasBatchName && batchName != "" {
			sub = sub.Where("b.name = ?", batchName)
			delete(filter, "batch")
		}
		db = db.Where("chapters.id IN (?)", sub)
	}

	// Apply other filters
	for key, value := range filter {
		if key == "search" {
			searchVal := "%" + value.(string) + "%"
			db = db.Where("chapters.chapter_title ILIKE ?", searchVal)
		} else {
			db = db.Where("chapters."+key+" = ?", value)
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	err := db.Preload("Batches").Limit(limit).Offset(offset).Order("chapter_no asc").Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}
