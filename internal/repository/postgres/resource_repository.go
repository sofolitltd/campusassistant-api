package postgres

import (
	"campusassistant-api/internal/domain"
	"context"

	"gorm.io/gorm"
)

type resourceRepository struct {
	domain.Repository[domain.Resource]
	db *gorm.DB
}

func NewResourceRepository(db *gorm.DB) domain.Repository[domain.Resource] {
	return &resourceRepository{
		Repository: NewGormRepository[domain.Resource](db),
		db:         db,
	}
}

func (r *resourceRepository) GetAll(ctx context.Context, filter map[string]interface{}, limit, offset int) ([]domain.Resource, int64, error) {
	var entities []domain.Resource
	var count int64

	db := r.db.WithContext(ctx).Model(&domain.Resource{})

	// ── Batch filtering (join-based) ─────────────────────────────────────────
	// ── Batch filtering (join-based) ─────────────────────────────────────────
	batchID, hasBatchID := filter["batch_id"]
	batchName, hasBatchName := filter["batch"]

	// Handle Batch Filtering via subquery for cleaner DISTINCT handling in COUNT
	if hasBatchID || hasBatchName {
		sub := r.db.Table("resource_batches rb").
			Select("rb.resource_id").
			Joins("JOIN batches b ON b.id = rb.batch_id")

		if hasBatchID && batchID != "" {
			sub = sub.Where("b.id = ?", batchID)
			delete(filter, "batch_id")
		}
		if hasBatchName && batchName != "" {
			sub = sub.Where("b.name = ?", batchName)
			delete(filter, "batch")
		}
		db = db.Where("resources.id IN (?)", sub)
	}

	// ── Status visibility ────────────────────────────────────────────────────
	// If a specific status is requested (e.g. from admin review queue), honour it.
	// Otherwise default to showing only published resources.
	if _, hasStatus := filter["status"]; !hasStatus {
		db = db.Where("resources.status = ?", domain.ResourceStatusPublished)
	}

	// ── Uploader filter (for "My Submissions" screen) ────────────────────────
	// uploader_uid is a string filter — handled generically below.

	// ── Apply remaining filters ──────────────────────────────────────────────
	for key, value := range filter {
		switch key {
		case "search":
			searchVal := "%" + value.(string) + "%"
			resType, hasType := filter["type"]

			if hasType && resType == "book" {
				// For Library: Search Book Title, Author (metadata), Course Code, and Course Name (via subquery to avoid duplicates)
				db = db.Where("(resources.title ILIKE ? OR resources.description ILIKE ? OR CAST(resources.metadata->>'author' AS TEXT) ILIKE ? OR resources.course_code ILIKE ? OR EXISTS (SELECT 1 FROM courses WHERE courses.course_code = resources.course_code AND courses.course_title ILIKE ?))",
					searchVal, searchVal, searchVal, searchVal, searchVal)
			} else {
				// Generic Search: Title, Description, and Course Code
				db = db.Where("resources.title ILIKE ? OR resources.description ILIKE ? OR resources.course_code ILIKE ?",
					searchVal, searchVal, searchVal)
			}
		case "year":
			// Search for specific year in the JSONB array (e.g. "2024")
			yearVal := "[\"" + value.(string) + "\"]"
			db = db.Where("resources.years @> ?", yearVal)
		case "tags":
			// Filter by a single tag string
			db = db.Where("? = ANY(resources.tags)", value)
		default:
			db = db.Where("resources."+key+" = ?", value)
		}
	}

	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	err := db.Preload("Batches").
		Order("resources.created_at DESC").
		Limit(limit).Offset(offset).
		Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}
