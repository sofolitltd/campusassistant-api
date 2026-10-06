package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"campusassistant-api/internal/domain"

	"gorm.io/gorm"
)

// CommissionService decides what fee a seller pays: their own rate, or the
// promotional rate during the new-seller window.
type CommissionService struct {
	db *gorm.DB
}

func NewCommissionService(db *gorm.DB) *CommissionService { return &CommissionService{db: db} }

const defaultPromoDays = 60

// Policy returns the stored policy, or an inactive default if none was saved.
func (s *CommissionService) Policy(ctx context.Context) (*domain.CommissionPolicy, error) {
	var p domain.CommissionPolicy
	err := s.db.WithContext(ctx).Order("created_at asc").First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.CommissionPolicy{PromoDays: defaultPromoDays}, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SetPolicy saves the single policy row.
func (s *CommissionService) SetPolicy(ctx context.Context, active bool, rate float64, days int) (*domain.CommissionPolicy, error) {
	if math.IsNaN(rate) || rate < 0 || rate > 100 {
		return nil, fmt.Errorf("promo rate must be between 0 and 100")
	}
	if days < 1 || days > 730 {
		return nil, fmt.Errorf("promo length must be between 1 and 730 days")
	}
	var p domain.CommissionPolicy
	err := s.db.WithContext(ctx).Order("created_at asc").First(&p).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	p.PromoActive, p.PromoRate, p.PromoDays = active, rate, days
	if err := s.db.WithContext(ctx).Save(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// CommissionInfo is what a seller sees about their fee.
type CommissionInfo struct {
	Rate          float64    `json:"rate"`      // % charged on new orders right now
	BaseRate      float64    `json:"base_rate"` // % after any promo ends
	OnPromo       bool       `json:"on_promo"`
	PromoEndsAt   *time.Time `json:"promo_ends_at,omitempty"`
	PromoDaysLeft int        `json:"promo_days_left"`
}

// Describe computes the fee that applies to m at time now.
func (s *CommissionService) Describe(ctx context.Context, m *domain.Merchant, now time.Time) (CommissionInfo, error) {
	info := CommissionInfo{Rate: m.CommissionRate, BaseRate: m.CommissionRate}
	if m.IsPlatform || m.Status != domain.MerchantStatusApproved {
		return info, nil
	}
	p, err := s.Policy(ctx)
	if err != nil {
		return info, err
	}
	if !p.PromoActive {
		return info, nil
	}
	start := m.CreatedAt
	if m.ApprovedAt != nil {
		start = *m.ApprovedAt
	}
	ends := start.AddDate(0, 0, p.PromoDays)
	if !now.Before(ends) {
		return info, nil
	}
	// A promo only ever lowers the fee.
	if p.PromoRate < m.CommissionRate {
		info.Rate = p.PromoRate
	}
	info.OnPromo = true
	info.PromoEndsAt = &ends
	info.PromoDaysLeft = int(math.Ceil(ends.Sub(now).Hours() / 24))
	return info, nil
}

// EffectiveRate is the percentage to snapshot on an order line sold by m.
// On a lookup error it falls back to the merchant's own rate: never charge
// less than agreed because the policy couldn't be read.
func (s *CommissionService) EffectiveRate(ctx context.Context, m *domain.Merchant) float64 {
	info, err := s.Describe(ctx, m, time.Now())
	if err != nil {
		return m.CommissionRate
	}
	return info.Rate
}
