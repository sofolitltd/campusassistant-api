package postgres

import (
	"campusassistant-api/internal/domain"
	"context"

	"gorm.io/gorm"
)

type semesterRepository struct {
	domain.Repository[domain.Semester]
	db *gorm.DB
}

func NewSemesterRepository(db *gorm.DB) domain.Repository[domain.Semester] {
	return &semesterRepository{
		Repository: NewGormRepository[domain.Semester](db),
		db:         db,
	}
}

func (r *semesterRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Semester, int64, error) {
	var entities []domain.Semester
	var count int64

	db := r.db.WithContext(ctx).Model(&domain.Semester{})

	// Handle Batch Filtering
	batchID, hasBatchID := filter["batch_id"]
	batchName, hasBatchName := filter["batch"]

	// Handle Batch Filtering via subquery for cleaner DISTINCT handling in COUNT
	if hasBatchID || hasBatchName {
		sub := r.db.Table("semester_batches sb").
			Select("sb.semester_id").
			Joins("JOIN batches b ON b.id = sb.batch_id")

		if hasBatchID && batchID != "" {
			sub = sub.Where("b.id = ?", batchID)
			delete(filter, "batch_id")
		}
		if hasBatchName && batchName != "" {
			sub = sub.Where("b.name = ?", batchName)
			delete(filter, "batch")
		}
		db = db.Where("semesters.id IN (?)", sub)
	}

	// Apply other filters
	for key, value := range filter {
		if key == "search" {
			searchVal := "%" + value.(string) + "%"
			db = db.Where("semesters.name ILIKE ?", searchVal)
		} else {
			db = db.Where("semesters."+key+" = ?", value)
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	err := db.Preload("Batches").Limit(limit).Offset(offset).Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}
