package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type lostFoundRepository struct {
	db *gorm.DB
}

func NewLostFoundRepository(db *gorm.DB) domain.LostFoundRepository {
	return &lostFoundRepository{db: db}
}

func (r *lostFoundRepository) GetAllItems(ctx context.Context, filter domain.LostFoundFilter) ([]domain.LostFoundItem, int64, error) {
	var items []domain.LostFoundItem
	q := r.db.WithContext(ctx).Model(&domain.LostFoundItem{}).
		Preload("Targets").Preload("Category").Preload("Poster")

	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Type != "" {
		q = q.Where("type = ?", filter.Type)
	}
	if filter.CategoryID != uuid.Nil {
		q = q.Where("category_id = ?", filter.CategoryID)
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("title ILIKE ? OR description ILIKE ? OR location ILIKE ?", like, like, like)
	}

	var count int64
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	err := q.Order("created_at desc").Limit(limit).Offset(filter.Offset).Find(&items).Error
	return items, count, err
}

func (r *lostFoundRepository) GetItemByID(ctx context.Context, id uuid.UUID) (*domain.LostFoundItem, error) {
	var item domain.LostFoundItem
	err := r.db.WithContext(ctx).Preload("Targets").Preload("Category").Preload("Poster").First(&item, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *lostFoundRepository) CreateItem(ctx context.Context, item *domain.LostFoundItem) error {
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *lostFoundRepository) UpdateItem(ctx context.Context, item *domain.LostFoundItem) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Delete old targets first to handle multi-select updates correctly
		// (same reasoning as productRepository.UpdateProduct).
		if err := tx.Where("item_id = ?", item.ID).Delete(&domain.LostFoundItemTarget{}).Error; err != nil {
			return err
		}
		return tx.Save(item).Error
	})
}

func (r *lostFoundRepository) SetItemStatus(ctx context.Context, id uuid.UUID, status domain.LostFoundStatus, removalReason string) error {
	updates := map[string]interface{}{"status": status}
	if status == domain.LostFoundStatusRemoved {
		updates["removal_reason"] = removalReason
	}
	if status == domain.LostFoundStatusResolved {
		updates["resolved_at"] = gorm.Expr("NOW()")
	}
	return r.db.WithContext(ctx).Model(&domain.LostFoundItem{}).Where("id = ?", id).Updates(updates).Error
}

func (r *lostFoundRepository) DeleteItem(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.LostFoundItem{}, id).Error
}

func (r *lostFoundRepository) GetItemsByPoster(ctx context.Context, posterID uuid.UUID) ([]domain.LostFoundItem, error) {
	var items []domain.LostFoundItem
	err := r.db.WithContext(ctx).Preload("Targets").Preload("Category").
		Where("poster_id = ?", posterID).
		Order("created_at desc").Find(&items).Error
	return items, err
}

func (r *lostFoundRepository) GetItemsByLocation(ctx context.Context, universityID, departmentID, categoryID uuid.UUID, itemType, search string) ([]domain.LostFoundItem, error) {
	var items []domain.LostFoundItem
	q := r.db.WithContext(ctx).
		Distinct("lost_found_items.*").
		Joins("LEFT JOIN lost_found_item_targets ON lost_found_item_targets.item_id = lost_found_items.id").
		Where("lost_found_items.status = ?", domain.LostFoundStatusOpen).
		Where("lost_found_item_targets.id IS NULL OR (lost_found_item_targets.university_id = ? AND (lost_found_item_targets.department_id = ? OR lost_found_item_targets.department_id = ?))",
			universityID, departmentID, uuid.Nil)

	if categoryID != uuid.Nil {
		q = q.Where("lost_found_items.category_id = ?", categoryID)
	}
	if itemType != "" {
		q = q.Where("lost_found_items.type = ?", itemType)
	}
	if search != "" {
		like := "%" + search + "%"
		q = q.Where("lost_found_items.title ILIKE ? OR lost_found_items.description ILIKE ? OR lost_found_items.location ILIKE ?", like, like, like)
	}

	err := q.Order("lost_found_items.created_at desc").
		Preload("Category").
		Preload("Poster").
		Find(&items).Error
	return items, err
}

func (r *lostFoundRepository) CreateClaim(ctx context.Context, claim *domain.LostFoundClaim) error {
	return r.db.WithContext(ctx).Create(claim).Error
}

func (r *lostFoundRepository) GetClaimByID(ctx context.Context, id uuid.UUID) (*domain.LostFoundClaim, error) {
	var claim domain.LostFoundClaim
	err := r.db.WithContext(ctx).Preload("Claimer").First(&claim, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &claim, nil
}

func (r *lostFoundRepository) GetClaimsByItem(ctx context.Context, itemID uuid.UUID) ([]domain.LostFoundClaim, error) {
	var claims []domain.LostFoundClaim
	err := r.db.WithContext(ctx).Preload("Claimer").
		Where("item_id = ?", itemID).
		Order("created_at desc").Find(&claims).Error
	return claims, err
}

func (r *lostFoundRepository) SetClaimStatus(ctx context.Context, id uuid.UUID, status domain.LostFoundClaimStatus) error {
	return r.db.WithContext(ctx).Model(&domain.LostFoundClaim{}).Where("id = ?", id).Update("status", status).Error
}

func (r *lostFoundRepository) CreateReport(ctx context.Context, report *domain.LostFoundReport) error {
	return r.db.WithContext(ctx).Create(report).Error
}

func (r *lostFoundRepository) GetAllReports(ctx context.Context, status domain.LostFoundReportStatus) ([]domain.LostFoundReport, error) {
	var reports []domain.LostFoundReport
	q := r.db.WithContext(ctx).Preload("Item").Preload("Reporter")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Order("created_at desc").Find(&reports).Error
	return reports, err
}

func (r *lostFoundRepository) SetReportStatus(ctx context.Context, id uuid.UUID, status domain.LostFoundReportStatus) error {
	return r.db.WithContext(ctx).Model(&domain.LostFoundReport{}).Where("id = ?", id).Update("status", status).Error
}
