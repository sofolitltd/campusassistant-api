package postgres

import (
	"campusassistant-api/internal/domain"
	"context"

	"gorm.io/gorm"
)

type courseRepository struct {
	domain.Repository[domain.Course]
	db *gorm.DB
}

func NewCourseRepository(db *gorm.DB) domain.Repository[domain.Course] {
	return &courseRepository{
		Repository: NewGormRepository[domain.Course](db),
		db:         db,
	}
}

func (r *courseRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Course, int64, error) {
	var entities []domain.Course
	var count int64

	db := r.db.WithContext(ctx).Model(&domain.Course{})

	// Handle Batch Filtering
	batchID, hasBatchID := filter["batch_id"]
	batchName, hasBatchName := filter["batch"]

	// Handle Batch Filtering via subquery for cleaner DISTINCT handling in COUNT
	if hasBatchID || hasBatchName {
		sub := r.db.Table("course_batches cb").
			Select("cb.course_id").
			Joins("JOIN batches b ON b.id = cb.batch_id")

		if hasBatchID && batchID != "" {
			sub = sub.Where("b.id = ?", batchID)
			delete(filter, "batch_id")
		}
		if hasBatchName && batchName != "" {
			sub = sub.Where("b.name = ?", batchName)
			delete(filter, "batch")
		}
		db = db.Where("courses.id IN (?)", sub)
	}

	// Apply other filters with prefix to avoid ambiguity
	for key, value := range filter {
		if key == "search" {
			searchVal := "%" + value.(string) + "%"
			db = db.Where("courses.course_title ILIKE ? OR courses.course_code ILIKE ?", searchVal, searchVal)
		} else if key == "semester_id" {
			db = db.Where("courses.semester_id = ?", value)
		} else {
			db = db.Where("courses."+key+" = ?", value)
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	err := db.Preload("Batches").Preload("CourseCategory").Preload("Semester").Limit(limit).Offset(offset).Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}
