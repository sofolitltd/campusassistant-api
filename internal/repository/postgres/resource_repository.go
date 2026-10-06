package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
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

// attachResourceCreators back-fills Resource.Creator from
// users.id = resources.created_by_id.
//
// This is an explicit IN query instead of a GORM association preload because
// the association is unreliable: User embeds Base (which has a CreatedByID
// field), so GORM's belongs-to resolution for `Creator *User
// gorm:"foreignKey:CreatedByID"` picks users.created_by_id as the reference
// column and the preload silently matches nothing. A manual query is the least
// surprising fix and keeps the created_by_id column on the Resource read path
// untouched.
func attachResourceCreators(ctx context.Context, db *gorm.DB, entities []domain.Resource) error {
	ids := make([]uuid.UUID, 0, len(entities))
	for _, e := range entities {
		if e.CreatedByID != uuid.Nil {
			ids = append(ids, e.CreatedByID)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	var users []domain.User
	if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return err
	}

	userMap := make(map[uuid.UUID]*domain.User, len(users))
	for i := range users {
		userMap[users[i].ID] = &users[i]
	}

	for i := range entities {
		if u, ok := userMap[entities[i].CreatedByID]; ok {
			entities[i].Creator = u
		}
	}
	return nil
}

func (r *resourceRepository) Create(ctx context.Context, entity *domain.Resource) error {
	batchIDs := entity.BatchIDs
	entity.BatchIDs = nil
	entity.Batches = nil

	if err := r.db.WithContext(ctx).Create(entity).Error; err != nil {
		return err
	}

	if len(batchIDs) > 0 {
		var batches []domain.Batch
		if err := r.db.Where("id IN ?", batchIDs).Find(&batches).Error; err != nil {
			return err
		}
		if err := r.db.Model(entity).Association("Batches").Replace(batches); err != nil {
			return err
		}
	}

	return nil
}

func (r *resourceRepository) Update(ctx context.Context, entity *domain.Resource) error {
	batchIDs := entity.BatchIDs
	entity.BatchIDs = nil
	entity.Batches = nil

	if err := r.db.WithContext(ctx).Save(entity).Error; err != nil {
		return err
	}

	if batchIDs != nil {
		var batches []domain.Batch
		if len(batchIDs) > 0 {
			if err := r.db.Where("id IN ?", batchIDs).Find(&batches).Error; err != nil {
				return err
			}
		}
		if err := r.db.Model(entity).Association("Batches").Replace(batches); err != nil {
			return err
		}
	}

	return nil
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

	// Creator is back-filled via attachResourceCreators below — the GORM
	// association preload matches nothing (see attachResourceCreators).
	err := db.Preload("Batches").
		Order("resources.created_at DESC").
		Limit(limit).Offset(offset).
		Find(&entities).Error
	if err != nil {
		return nil, 0, err
	}

	if err := attachResourceCreators(ctx, r.db, entities); err != nil {
		return nil, 0, err
	}

	return entities, count, nil
}

func (r *resourceRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Resource, error) {
	var entity domain.Resource
	// Batches only — Creator is back-filled manually. Preload(clause.Associations)
	// would also attempt the broken Creator association (nil result).
	if err := r.db.WithContext(ctx).Preload("Batches").First(&entity, "id = ?", id).Error; err != nil {
		return nil, err
	}

	entities := []domain.Resource{entity}
	if err := attachResourceCreators(ctx, r.db, entities); err != nil {
		return nil, err
	}
	entity = entities[0]
	return &entity, nil
}
