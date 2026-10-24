package postgres

import (
	"campusassistant-api/internal/domain"
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// Delete and HardDelete refuse while live courses still point at the level:
// a deleted level hides its courses from the admin (they're reached through
// the level and aren't "without level" either), so they must be moved first.
func (r *levelRepository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.ensureNoCourses(ctx, id); err != nil {
		return err
	}
	return r.Repository.Delete(ctx, id)
}

func (r *levelRepository) HardDelete(ctx context.Context, id uuid.UUID) error {
	if err := r.ensureNoCourses(ctx, id); err != nil {
		return err
	}
	return r.Repository.HardDelete(ctx, id)
}

func (r *levelRepository) ensureNoCourses(ctx context.Context, id uuid.UUID) error {
	var n int64
	if err := r.db.WithContext(ctx).Model(&domain.Course{}).Where("level_id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: this level has %d course(s) — move them to another level or delete them first", domain.ErrInUse, n)
	}
	return nil
}

// Update replaces the level's batches with BatchIDs when they are sent. The
// generic Save (via Level.BeforeSave) only ever adds join rows, so unticking a
// batch in the admin never removed it. nil BatchIDs leaves batches untouched.
func (r *levelRepository) Update(ctx context.Context, entity *domain.Level) error {
	batchIDs := entity.BatchIDs
	entity.BatchIDs = nil
	entity.Batches = nil

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit(clause.Associations).Save(entity).Error; err != nil {
			return err
		}
		if batchIDs == nil {
			return nil
		}
		var batches []domain.Batch
		if len(batchIDs) > 0 {
			if err := tx.Where("id IN ?", batchIDs).Find(&batches).Error; err != nil {
				return err
			}
		}
		return tx.Model(entity).Association("Batches").Replace(batches)
	})
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
