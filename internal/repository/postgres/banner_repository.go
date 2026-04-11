package postgres

import (
	"context"
	"time"

	"campusassistant-api/internal/domain"

	"gorm.io/gorm"
)

type bannerRepository struct {
	domain.Repository[domain.Banner]
	db *gorm.DB
}

func NewBannerRepository(db *gorm.DB) domain.Repository[domain.Banner] {
	return &bannerRepository{
		Repository: NewGormRepository[domain.Banner](db),
		db:         db,
	}
}

func (r *bannerRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Banner, int64, error) {
	var entities []domain.Banner
	var count int64

	db := r.db.WithContext(ctx).Model(&domain.Banner{}).Preload("Targets")

	// Default to targeting mode unless explicitly told otherwise
	mode, hasMode := filter["mode"]
	isAdminQuery := hasMode && mode == "admin"
	delete(filter, "mode")

	if !isAdminQuery {
		now := time.Now()
		db = db.Where("is_active = ?", true).
			Where("start_at <= ? AND end_at >= ?", now, now)

		// TARGETING:
		// 1. Scope is Global
		// 2. OR matches specific university/department targets
		uniID, hasUni := filter["university_id"]
		deptID, hasDept := filter["department_id"]

		subQuery := r.db.Model(&domain.BannerTarget{})
		applyTargeting := false

		if hasUni && hasDept && uniID != "" && deptID != "" {
			subQuery = subQuery.Where("university_id = ? OR department_id = ?", uniID, deptID)
			applyTargeting = true
		} else if hasUni && uniID != "" {
			subQuery = subQuery.Where("university_id = ?", uniID)
			applyTargeting = true
		} else if hasDept && deptID != "" {
			subQuery = subQuery.Where("department_id = ?", deptID)
			applyTargeting = true
		}

		if applyTargeting {
			db = db.Where(
				r.db.Where("target_scope = ?", "Global").
					Or("id IN (?)", subQuery.Select("banner_id")),
			)
		} else {
			// Guest or no data provided? Show only Global
			db = db.Where("target_scope = ?", "Global")
		}

		// Standard filter fields should be removed from map
		delete(filter, "university_id")
		delete(filter, "department_id")
	}

	if len(filter) > 0 {
		db = db.Where(filter)
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	err := db.Order("priority desc").Limit(limit).Offset(offset).Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}
