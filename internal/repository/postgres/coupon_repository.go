package postgres

import (
	"context"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type couponRepository struct {
	db *gorm.DB
}

func NewCouponRepository(db *gorm.DB) domain.CouponRepository {
	return &couponRepository{db: db}
}

func (r *couponRepository) GetByCode(ctx context.Context, code string) (*domain.CouponCode, error) {
	var coupon domain.CouponCode
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&coupon).Error
	if err != nil {
		return nil, err
	}
	return &coupon, nil
}

func (r *couponRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.CouponCode, error) {
	var coupon domain.CouponCode
	err := r.db.WithContext(ctx).First(&coupon, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &coupon, nil
}

func (r *couponRepository) Create(ctx context.Context, coupon *domain.CouponCode) error {
	return r.db.WithContext(ctx).Create(coupon).Error
}

func (r *couponRepository) Update(ctx context.Context, coupon *domain.CouponCode) error {
	return r.db.WithContext(ctx).Save(coupon).Error
}

func (r *couponRepository) GetAll(ctx context.Context) ([]domain.CouponCode, error) {
	var coupons []domain.CouponCode
	err := r.db.WithContext(ctx).Order("created_at desc").Find(&coupons).Error
	return coupons, err
}
