package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrFeatureNotEntitled  = errors.New("your plan does not include this feature")
	ErrFeatureLimitReached = errors.New("usage limit for this feature has been reached")
	ErrInvalidEntitlement  = errors.New("invalid entitlement")
)

// EntitlementService answers "may this user use this feature, and how much
// of it?". It replaces scattering `user.IsPro` checks: a plan declares its
// features (PlanEntitlement rows), the user's active subscription picks the
// plan, and usage of capped features is metered per period.
//
// Backward compatible: a Pro user whose plan has no entitlement rows gets
// every domain.DefaultProFeatures, unlimited — i.e. exactly "Pro = on".
type EntitlementService struct {
	db *gorm.DB
}

func NewEntitlementService(db *gorm.DB) *EntitlementService {
	return &EntitlementService{db: db}
}

func periodKey(period string, now time.Time) string {
	now = now.UTC()
	switch period {
	case domain.PeriodDaily:
		return now.Format("2006-01-02")
	case domain.PeriodMonth:
		return now.Format("2006-01")
	default:
		return "all"
	}
}

// activePlan returns whether the user is currently Pro and which plan
// that comes from (nil for a Pro flag with no matching subscription row).
func activePlan(db *gorm.DB, userID uuid.UUID, now time.Time) (isPro bool, planID *uuid.UUID, err error) {
	var user domain.User
	if err := db.Select("id", "is_pro", "pro_expiry").First(&user, "id = ?", userID).Error; err != nil {
		return false, nil, err
	}
	if !user.IsPro || (user.ProExpiry != nil && !user.ProExpiry.After(now)) {
		return false, nil, nil
	}
	var sub domain.UserSubscription
	err = db.Where("user_id = ? AND refunded_at IS NULL AND (end_date IS NULL OR end_date > ?)", userID, now).
		Order("created_at desc").First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, &sub.PlanID, nil
}

func (s *EntitlementService) resolve(ctx context.Context, userID uuid.UUID) (*domain.UserEntitlements, map[string]domain.PlanEntitlement, error) {
	db := s.db.WithContext(ctx)
	now := time.Now()
	isPro, planID, err := activePlan(db, userID, now)
	if err != nil {
		return nil, nil, err
	}

	out := &domain.UserEntitlements{IsPro: isPro, PlanID: planID, Features: map[string]domain.FeatureAccess{}}
	grants := map[string]domain.PlanEntitlement{}

	if isPro {
		var rows []domain.PlanEntitlement
		if planID != nil {
			if err := db.Where("plan_id = ?", *planID).Find(&rows).Error; err != nil {
				return nil, nil, err
			}
		}
		if len(rows) == 0 {
			for _, f := range domain.DefaultProFeatures {
				rows = append(rows, domain.PlanEntitlement{Feature: f, Limit: domain.LimitUnlimited, Period: domain.PeriodNone})
			}
		}
		for _, r := range rows {
			grants[r.Feature] = r
		}
	}

	// Every known feature appears in the response, denied unless granted.
	known := map[string]bool{}
	for _, f := range domain.DefaultProFeatures {
		known[f] = true
	}
	for f := range grants {
		known[f] = true
	}
	for f := range known {
		g, ok := grants[f]
		if !ok || g.Limit == 0 {
			out.Features[f] = domain.FeatureAccess{Allowed: false, Limit: 0, Period: domain.PeriodNone, Remaining: 0}
			continue
		}
		access := domain.FeatureAccess{Allowed: true, Limit: g.Limit, Period: g.Period, Remaining: domain.LimitUnlimited}
		if g.Limit != domain.LimitUnlimited {
			var c domain.UsageCounter
			err := db.Where("user_id = ? AND feature = ? AND period_key = ?", userID, f, periodKey(g.Period, now)).First(&c).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, err
			}
			access.Used = c.Used
			access.Remaining = g.Limit - c.Used
			if access.Remaining < 0 {
				access.Remaining = 0
			}
		}
		out.Features[f] = access
	}
	return out, grants, nil
}

// ForUser returns everything the user is entitled to (for GET /my/entitlements).
func (s *EntitlementService) ForUser(ctx context.Context, userID uuid.UUID) (*domain.UserEntitlements, error) {
	e, _, err := s.resolve(ctx, userID)
	return e, err
}

// Check reports the user's access to one feature. It does not consume usage.
func (s *EntitlementService) Check(ctx context.Context, userID uuid.UUID, feature string) (domain.FeatureAccess, error) {
	e, _, err := s.resolve(ctx, userID)
	if err != nil {
		return domain.FeatureAccess{}, err
	}
	return e.Features[feature], nil // unknown features are denied (zero value)
}

// Consume spends n units of a capped feature. The cap is enforced by a
// conditional UPDATE, so concurrent requests can't jointly exceed it.
// Uncapped features succeed without recording anything.
func (s *EntitlementService) Consume(ctx context.Context, userID uuid.UUID, feature string, n int) error {
	if n <= 0 {
		return fmt.Errorf("%w: n must be positive", ErrInvalidEntitlement)
	}
	e, grants, err := s.resolve(ctx, userID)
	if err != nil {
		return err
	}
	if !e.Features[feature].Allowed {
		return ErrFeatureNotEntitled
	}
	g := grants[feature]
	if g.Limit == domain.LimitUnlimited {
		return nil
	}

	db := s.db.WithContext(ctx)
	key := periodKey(g.Period, time.Now())
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&domain.UsageCounter{UserID: userID, Feature: feature, PeriodKey: key}).Error; err != nil {
		return err
	}
	res := db.Model(&domain.UsageCounter{}).
		Where("user_id = ? AND feature = ? AND period_key = ? AND used + ? <= ?", userID, feature, key, n, g.Limit).
		UpdateColumn("used", gorm.Expr("used + ?", n))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrFeatureLimitReached
	}
	return nil
}

// PlanEntitlements lists the explicit entitlements of a plan.
func (s *EntitlementService) PlanEntitlements(ctx context.Context, planID uuid.UUID) ([]domain.PlanEntitlement, error) {
	var rows []domain.PlanEntitlement
	err := s.db.WithContext(ctx).Where("plan_id = ?", planID).Order("feature asc").Find(&rows).Error
	return rows, err
}

// SetPlanEntitlements replaces a plan's entitlements. An empty set restores
// the default ("Pro = every default feature, unlimited").
func (s *EntitlementService) SetPlanEntitlements(ctx context.Context, planID uuid.UUID, items []domain.PlanEntitlement) ([]domain.PlanEntitlement, error) {
	seen := map[string]bool{}
	for i := range items {
		it := &items[i]
		if it.Feature == "" || len(it.Feature) > 60 || seen[it.Feature] {
			return nil, fmt.Errorf("%w: feature %q is empty, too long or duplicated", ErrInvalidEntitlement, it.Feature)
		}
		seen[it.Feature] = true
		if it.Limit < domain.LimitUnlimited {
			return nil, fmt.Errorf("%w: limit for %s must be -1 (unlimited) or >= 0", ErrInvalidEntitlement, it.Feature)
		}
		switch it.Period {
		case "":
			it.Period = domain.PeriodNone
		case domain.PeriodNone, domain.PeriodDaily, domain.PeriodMonth:
		default:
			return nil, fmt.Errorf("%w: period for %s must be none, daily or month", ErrInvalidEntitlement, it.Feature)
		}
		if it.Limit == domain.LimitUnlimited {
			it.Period = domain.PeriodNone
		}
		it.ID, it.PlanID = uuid.Nil, planID
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan domain.SubscriptionPlan
		if err := tx.First(&plan, "id = ?", planID).Error; err != nil {
			return ErrPlanNotFound
		}
		// Hard delete: the unique (plan_id, feature) index would otherwise
		// collide with soft-deleted rows when the same feature is re-added.
		if err := tx.Unscoped().Where("plan_id = ?", planID).Delete(&domain.PlanEntitlement{}).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		return tx.Create(&items).Error
	})
	return items, err
}
