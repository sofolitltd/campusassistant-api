package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func makePro(t *testing.T, e *env, u domain.User, plan domain.SubscriptionPlan, expiry *time.Time) {
	t.Helper()
	e.db.Model(&domain.User{}).Where("id = ?", u.ID).Updates(map[string]any{"is_pro": true, "pro_expiry": expiry})
	end := expiry
	e.db.Create(&domain.UserSubscription{UserID: u.ID, PlanID: plan.ID, Plan: plan.Title, StartDate: time.Now(), EndDate: end, GrantReason: "payment"})
}

func TestEntitlements_FreeUserHasNothing_ProDefaultsToEverything(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ent := NewEntitlementService(e.db)
	free, pro := e.user(t), e.user(t)
	plan := e.plan(t, 100, 30, false)
	soon := time.Now().Add(24 * time.Hour)
	makePro(t, e, pro, plan, &soon)

	got, err := ent.ForUser(ctx, free.ID)
	if err != nil || got.IsPro {
		t.Fatalf("free: %+v err=%v", got, err)
	}
	for f, a := range got.Features {
		if a.Allowed {
			t.Fatalf("free user allowed %s", f)
		}
	}
	// Plan with no entitlement rows keeps legacy behaviour: Pro = everything on.
	for _, f := range domain.DefaultProFeatures {
		a, _ := ent.Check(ctx, pro.ID, f)
		if !a.Allowed || a.Limit != domain.LimitUnlimited || a.Remaining != -1 {
			t.Fatalf("pro %s = %+v, want allowed+unlimited", f, a)
		}
	}
	if a, _ := ent.Check(ctx, pro.ID, "made_up_feature"); a.Allowed {
		t.Fatal("unknown feature must be denied")
	}
}

func TestEntitlements_ExpiredProIsTreatedAsFree(t *testing.T) {
	e := newEnv(t)
	ent := NewEntitlementService(e.db)
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	past := time.Now().Add(-time.Hour)
	makePro(t, e, u, plan, &past) // is_pro still true: the hourly sweeper hasn't run yet

	if a, _ := ent.Check(context.Background(), u.ID, domain.FeatureAdFree); a.Allowed {
		t.Fatal("expired Pro must not keep entitlements while waiting for the sweeper")
	}
}

func TestEntitlements_PlanDefinesFeaturesAndCaps(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ent := NewEntitlementService(e.db)
	u := e.user(t)
	basic := e.plan(t, 50, 30, false)
	makePro(t, e, u, basic, nil)

	_, err := ent.SetPlanEntitlements(ctx, basic.ID, []domain.PlanEntitlement{
		{Feature: domain.FeatureAdFree, Limit: domain.LimitUnlimited},
		{Feature: domain.FeatureUnlimitedDownloads, Limit: 2, Period: domain.PeriodDaily},
	})
	if err != nil {
		t.Fatal(err)
	}

	if a, _ := ent.Check(ctx, u.ID, domain.FeatureAdFree); !a.Allowed {
		t.Fatal("ad_free should be allowed")
	}
	// Not listed on this plan → denied (explicit plans don't fall back to defaults).
	if a, _ := ent.Check(ctx, u.ID, domain.FeaturePremiumContent); a.Allowed {
		t.Fatal("premium_content is not on this plan")
	}
	for i := 0; i < 2; i++ {
		if err := ent.Consume(ctx, u.ID, domain.FeatureUnlimitedDownloads, 1); err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
	}
	if err := ent.Consume(ctx, u.ID, domain.FeatureUnlimitedDownloads, 1); !errors.Is(err, ErrFeatureLimitReached) {
		t.Fatalf("3rd consume: err = %v, want ErrFeatureLimitReached", err)
	}
	if a, _ := ent.Check(ctx, u.ID, domain.FeatureUnlimitedDownloads); a.Used != 2 || a.Remaining != 0 {
		t.Fatalf("access = %+v, want used 2 remaining 0", a)
	}
	if err := ent.Consume(ctx, u.ID, domain.FeaturePremiumContent, 1); !errors.Is(err, ErrFeatureNotEntitled) {
		t.Fatalf("unentitled consume: err = %v", err)
	}
}

func TestEntitlements_ConcurrentConsumeCannotExceedCap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ent := NewEntitlementService(e.db)
	u := e.user(t)
	plan := e.plan(t, 50, 30, false)
	makePro(t, e, u, plan, nil)
	ent.SetPlanEntitlements(ctx, plan.ID, []domain.PlanEntitlement{{Feature: domain.FeatureUnlimitedDownloads, Limit: 3, Period: domain.PeriodMonth}})

	ok := 0
	for _, err := range runConcurrently(10, func(int) error { return ent.Consume(ctx, u.ID, domain.FeatureUnlimitedDownloads, 1) }) {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrFeatureLimitReached) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 3 {
		t.Fatalf("%d consumes succeeded against a cap of 3", ok)
	}
}

func TestEntitlements_SetValidatesAndEmptyRestoresDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ent := NewEntitlementService(e.db)
	plan := e.plan(t, 50, 30, false)

	bad := [][]domain.PlanEntitlement{
		{{Feature: "", Limit: -1}},
		{{Feature: "x", Limit: -5}},
		{{Feature: "x", Limit: 1, Period: "weekly"}},
		{{Feature: "x", Limit: 1}, {Feature: "x", Limit: 2}},
	}
	for i, items := range bad {
		if _, err := ent.SetPlanEntitlements(ctx, plan.ID, items); !errors.Is(err, ErrInvalidEntitlement) {
			t.Fatalf("case %d: err = %v, want ErrInvalidEntitlement", i, err)
		}
	}
	if _, err := ent.SetPlanEntitlements(ctx, uuid.New(), nil); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("missing plan: err = %v", err)
	}
	// Re-adding a feature after removing it must not trip the unique index.
	for i := 0; i < 2; i++ {
		if _, err := ent.SetPlanEntitlements(ctx, plan.ID, []domain.PlanEntitlement{{Feature: "x", Limit: 1}}); err != nil {
			t.Fatalf("replace %d: %v", i, err)
		}
	}
	if _, err := ent.SetPlanEntitlements(ctx, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if rows, _ := ent.PlanEntitlements(ctx, plan.ID); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
}
