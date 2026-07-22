package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type merchantRepository struct {
	db *gorm.DB
}

func NewMerchantRepository(db *gorm.DB) domain.MerchantRepository {
	return &merchantRepository{db: db}
}

func (r *merchantRepository) GetAllMerchants(ctx context.Context, status domain.MerchantStatus) ([]domain.Merchant, error) {
	var merchants []domain.Merchant
	q := r.db.WithContext(ctx).Where("is_platform = ?", false)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Order("created_at desc").Find(&merchants).Error
	return merchants, err
}

func (r *merchantRepository) GetMerchantByID(ctx context.Context, id uuid.UUID) (*domain.Merchant, error) {
	var merchant domain.Merchant
	err := r.db.WithContext(ctx).First(&merchant, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &merchant, nil
}

func (r *merchantRepository) GetMerchantByUserID(ctx context.Context, userID uuid.UUID) (*domain.Merchant, error) {
	var merchant domain.Merchant
	err := r.db.WithContext(ctx).First(&merchant, "user_id = ?", userID).Error
	if err != nil {
		return nil, err
	}
	return &merchant, nil
}

// GetOrCreatePlatformMerchant returns the single synthetic Merchant row
// representing Campus Assistant's own in-house products, creating it on
// first use so in-house Products always have a valid MerchantID FK.
func (r *merchantRepository) GetOrCreatePlatformMerchant(ctx context.Context) (*domain.Merchant, error) {
	var merchant domain.Merchant
	err := r.db.WithContext(ctx).First(&merchant, "is_platform = ?", true).Error
	if err == nil {
		return &merchant, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	merchant = domain.Merchant{
		BusinessName:   "Campus Assistant",
		Description:    "In-house products sold directly by Campus Assistant.",
		CommissionRate: 0,
		Status:         domain.MerchantStatusApproved,
		IsPlatform:     true,
	}
	if err := r.db.WithContext(ctx).Create(&merchant).Error; err != nil {
		return nil, err
	}
	return &merchant, nil
}

func (r *merchantRepository) CreateMerchant(ctx context.Context, merchant *domain.Merchant) error {
	return r.db.WithContext(ctx).Create(merchant).Error
}

func (r *merchantRepository) UpdateMerchant(ctx context.Context, merchant *domain.Merchant) error {
	return r.db.WithContext(ctx).Save(merchant).Error
}

func (r *merchantRepository) SetMerchantStatus(ctx context.Context, id uuid.UUID, status domain.MerchantStatus, rejectionReason string) error {
	return r.db.WithContext(ctx).Model(&domain.Merchant{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": status, "rejection_reason": rejectionReason}).Error
}

func (r *merchantRepository) DeleteMerchant(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Delete(&domain.Merchant{}, id).Error
}
