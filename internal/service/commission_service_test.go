package service

import (
	"context"
	"testing"
	"time"

	"campusassistant-api/internal/domain"
)

func approvedAgo(t *testing.T, e *env, m domain.Merchant, ago time.Duration) domain.Merchant {
	t.Helper()
	if err := e.db.Exec("UPDATE merchants SET approved_at = ? WHERE id = ?", time.Now().Add(-ago), m.ID).Error; err != nil {
		t.Fatal(err)
	}
	return reload[domain.Merchant](t, e.db, m.ID)
}

func TestCommission_NoPolicyUsesMerchantRate(t *testing.T) {
	e := newEnv(t)
	svc := NewCommissionService(e.db)
	m := e.merchant(t, 12, false)
	if got := svc.EffectiveRate(context.Background(), &m); got != 12 {
		t.Fatalf("rate = %v, want 12", got)
	}
}

func TestCommission_PromoAppliesInsideWindowOnly(t *testing.T) {
	e := newEnv(t)
	svc := NewCommissionService(e.db)
	ctx := context.Background()
	if _, err := svc.SetPolicy(ctx, true, 0, 60); err != nil {
		t.Fatal(err)
	}

	fresh := approvedAgo(t, e, e.merchant(t, 10, false), 10*24*time.Hour)
	if got := svc.EffectiveRate(ctx, &fresh); got != 0 {
		t.Fatalf("new seller rate = %v, want promo 0", got)
	}
	info, _ := svc.Describe(ctx, &fresh, time.Now())
	if !info.OnPromo || info.BaseRate != 10 || info.PromoDaysLeft != 50 || info.PromoEndsAt == nil {
		t.Fatalf("describe = %+v", info)
	}

	old := approvedAgo(t, e, e.merchant(t, 10, false), 61*24*time.Hour)
	if got := svc.EffectiveRate(ctx, &old); got != 10 {
		t.Fatalf("seller past the window pays %v, want 10", got)
	}
	if info, _ := svc.Describe(ctx, &old, time.Now()); info.OnPromo {
		t.Fatalf("promo should be over: %+v", info)
	}
}

func TestCommission_PromoNeverRaisesTheFee(t *testing.T) {
	e := newEnv(t)
	svc := NewCommissionService(e.db)
	ctx := context.Background()
	_, _ = svc.SetPolicy(ctx, true, 15, 60) // promo "rate" higher than the seller's own
	m := approvedAgo(t, e, e.merchant(t, 5, false), time.Hour)
	if got := svc.EffectiveRate(ctx, &m); got != 5 {
		t.Fatalf("rate = %v, want 5 (promo must only lower the fee)", got)
	}
}

func TestCommission_InactivePlatformAndUnapproved(t *testing.T) {
	e := newEnv(t)
	svc := NewCommissionService(e.db)
	ctx := context.Background()
	_, _ = svc.SetPolicy(ctx, false, 0, 60)
	m := approvedAgo(t, e, e.merchant(t, 10, false), time.Hour)
	if got := svc.EffectiveRate(ctx, &m); got != 10 {
		t.Fatalf("inactive promo: rate = %v", got)
	}

	_, _ = svc.SetPolicy(ctx, true, 0, 60)
	pending := e.merchant(t, 10, false)
	pending.Status = domain.MerchantStatusPending
	if got := svc.EffectiveRate(ctx, &pending); got != 10 {
		t.Fatalf("a merchant who isn't approved gets no promo, got %v", got)
	}
}

func TestCommission_FallsBackToApplicationDateForOldMerchants(t *testing.T) {
	e := newEnv(t)
	svc := NewCommissionService(e.db)
	ctx := context.Background()
	_, _ = svc.SetPolicy(ctx, true, 0, 60)
	m := e.merchant(t, 10, false) // approved_at is NULL, created just now
	if got := svc.EffectiveRate(ctx, &m); got != 0 {
		t.Fatalf("rate = %v, want promo 0 counted from created_at", got)
	}
	e.backdate(t, "merchants", m.ID, 90*24*time.Hour)
	m = reload[domain.Merchant](t, e.db, m.ID)
	if got := svc.EffectiveRate(ctx, &m); got != 10 {
		t.Fatalf("old merchant rate = %v, want 10", got)
	}
}

func TestCommission_SetPolicyValidatesAndUpserts(t *testing.T) {
	e := newEnv(t)
	svc := NewCommissionService(e.db)
	ctx := context.Background()
	for _, bad := range []struct {
		rate float64
		days int
	}{{-1, 60}, {101, 60}, {0, 0}, {0, 1000}} {
		if _, err := svc.SetPolicy(ctx, true, bad.rate, bad.days); err == nil {
			t.Fatalf("SetPolicy(%v, %d) should fail", bad.rate, bad.days)
		}
	}
	_, _ = svc.SetPolicy(ctx, true, 1, 30)
	_, _ = svc.SetPolicy(ctx, false, 2, 45)
	if n := count(t, e.db, &domain.CommissionPolicy{}, ""); n != 1 {
		t.Fatalf("policy rows = %d, want a single row", n)
	}
	p, _ := svc.Policy(ctx)
	if p.PromoActive || p.PromoRate != 2 || p.PromoDays != 45 {
		t.Fatalf("policy = %+v", p)
	}
}

func TestMetrics(t *testing.T) {
	e := newEnv(t)
	ins := NewInsightsService(e.db)
	m1, m2 := e.merchant(t, 10, false), e.merchant(t, 10, false)
	e.product(t, m1, 100, 5)
	a, b := e.user(t), e.user(t)

	e.order(t, a, domain.PaymentMethodCashOnDelivery, domain.OrderStatusDelivered, orderLine{m1, 100, 1}) // a: 1st
	e.order(t, a, domain.PaymentMethodCashOnDelivery, domain.OrderStatusDelivered, orderLine{m1, 200, 1}) // a: repeat
	e.order(t, b, domain.PaymentMethodBkash, domain.OrderStatusPaid, orderLine{m2, 300, 1})
	e.order(t, b, domain.PaymentMethodBkash, domain.OrderStatusCancelled, orderLine{m2, 999, 1})

	got, err := ins.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ListedProducts != 1 || got.ActiveSellers != 1 {
		t.Fatalf("supply: %+v", got)
	}
	if got.Orders30d != 3 || got.GMV30d != 600 || got.Buyers30d != 2 || got.SellersWithSales != 2 {
		t.Fatalf("demand: %+v", got)
	}
	if got.RepeatBuyerRate != 50 {
		t.Fatalf("repeat rate = %v, want 50", got.RepeatBuyerRate)
	}
	if got.Delivered30d != 2 || got.Cancelled30d != 1 || got.SmoothDeliveryRate < 66 || got.SmoothDeliveryRate > 67 {
		t.Fatalf("health: %+v", got)
	}
}
