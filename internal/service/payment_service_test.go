package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func TestExecutePayment_ConcurrentExecutesGrantExactlyOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)

	res, err := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	e.bkash.pay(res.PaymentID)

	// Eight simultaneous "execute" calls — a double tap, retries, and the
	// app plus the reconciler all landing at once.
	errs := runConcurrently(8, func(int) error {
		_, err := e.payments.ExecutePayment(ctx, u.ID, res.PaymentID)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if n := count(t, e.db, &domain.UserSubscription{}, "user_id = ?", u.ID); n != 1 {
		t.Fatalf("subscriptions = %d, want exactly 1", n)
	}
	if n := count(t, e.db, &domain.Invoice{}, ""); n != 1 {
		t.Fatalf("invoices = %d, want 1", n)
	}
	if n := count(t, e.db, &domain.Journal{}, "kind = ?", KindSubscriptionPayment); n != 1 {
		t.Fatalf("revenue journals = %d, want 1", n)
	}
	got := reload[domain.User](t, e.db, u.ID)
	if !got.IsPro || got.ProExpiry == nil || got.ProExpiry.Before(time.Now().Add(29*24*time.Hour)) {
		t.Fatalf("user not pro for ~30 days: pro=%v expiry=%v", got.IsPro, got.ProExpiry)
	}
	tx := reload[domain.BkashTransaction](t, e.db, mustTxID(t, e, res.PaymentID))
	if tx.Status != domain.BkashStatusCompleted || tx.SubscriptionID == nil {
		t.Fatalf("tx = %s sub=%v, want completed with subscription", tx.Status, tx.SubscriptionID)
	}
	assertLedgerBalanced(t, e.db)
}

func mustTxID(t *testing.T, e *env, paymentID string) uuid.UUID {
	t.Helper()
	var tx domain.BkashTransaction
	if err := e.db.First(&tx, "payment_id = ?", paymentID).Error; err != nil {
		t.Fatal(err)
	}
	return tx.ID
}

func TestExecutePayment_UnpaidIsNotGranted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")

	// Client claims success without paying.
	_, err := e.payments.ExecutePayment(ctx, u.ID, res.PaymentID)
	if !errors.Is(err, ErrPaymentNotCompleted) {
		t.Fatalf("err = %v, want ErrPaymentNotCompleted", err)
	}
	if reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("user became pro without paying")
	}
}

func TestExecutePayment_RejectsOtherUsersPayment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, other := e.user(t), e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, owner.ID, plan.ID, "")
	e.bkash.pay(res.PaymentID)

	if _, err := e.payments.ExecutePayment(ctx, other.ID, res.PaymentID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestExecutePayment_AmountMismatchIsFlaggedNotGranted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")
	e.bkash.pay(res.PaymentID)
	e.bkash.amountOverride = "1" // bKash says only 1 taka moved

	_, err := e.payments.ExecutePayment(ctx, u.ID, res.PaymentID)
	if !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("err = %v, want ErrAmountMismatch", err)
	}
	if reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("user granted Pro despite amount mismatch")
	}
	// The review flag must survive (it would be lost if it rolled back).
	var tx domain.BkashTransaction
	e.db.First(&tx, "payment_id = ?", res.PaymentID)
	if tx.Status != domain.BkashStatusNeedsReview {
		t.Fatalf("status = %s, want needs_review", tx.Status)
	}
}

func TestSubscriptionRenewalStacksAndLifetimeIsNeverShortened(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	plan := e.plan(t, 100, 30, false)

	// Already Pro with 10 days left: a 30-day purchase ends in ~40 days.
	u := e.user(t)
	soon := time.Now().Add(10 * 24 * time.Hour)
	e.db.Model(&domain.User{}).Where("id = ?", u.ID).Updates(map[string]any{"is_pro": true, "pro_expiry": soon})
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")
	e.bkash.pay(res.PaymentID)
	if _, err := e.payments.ExecutePayment(ctx, u.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	got := reload[domain.User](t, e.db, u.ID)
	want := soon.AddDate(0, 0, 30)
	if got.ProExpiry == nil || got.ProExpiry.Sub(want).Abs() > time.Minute {
		t.Fatalf("expiry = %v, want ~%v", got.ProExpiry, want)
	}

	// Lifetime user buying a dated plan keeps lifetime (nil expiry).
	life := e.user(t)
	e.db.Model(&domain.User{}).Where("id = ?", life.ID).Update("is_pro", true)
	res2, _ := e.payments.CreatePayment(ctx, life.ID, plan.ID, "")
	e.bkash.pay(res2.PaymentID)
	if _, err := e.payments.ExecutePayment(ctx, life.ID, res2.PaymentID); err != nil {
		t.Fatal(err)
	}
	if reload[domain.User](t, e.db, life.ID).ProExpiry != nil {
		t.Fatal("lifetime user was given a dated expiry")
	}
}

// ─── coupons ─────────────────────────────────────────────────────────────

func TestCoupon_LastUseGoesToExactlyOneBuyer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	plan := e.plan(t, 100, 30, false)
	c := e.coupon(t, 1, 50)
	users := []domain.User{e.user(t), e.user(t), e.user(t), e.user(t)}

	errs := runConcurrently(len(users), func(i int) error {
		_, err := e.payments.CreatePayment(ctx, users[i].ID, plan.ID, c.Code)
		return err
	})
	ok, exhausted := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrCouponExhausted):
			exhausted++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || exhausted != len(users)-1 {
		t.Fatalf("ok=%d exhausted=%d, want 1 and %d", ok, exhausted, len(users)-1)
	}
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 1 {
		t.Fatalf("used_count = %d, want 1", got)
	}
}

func TestCoupon_ReleasedWhenCheckoutIsCancelled(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	c := e.coupon(t, 1, 50)

	res, err := e.payments.CreatePayment(ctx, u.ID, plan.ID, c.Code)
	if err != nil {
		t.Fatal(err)
	}
	if res.DiscountedAmount == nil || *res.DiscountedAmount != 50 {
		t.Fatalf("discounted = %v, want 50", res.DiscountedAmount)
	}
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 1 {
		t.Fatalf("reserved used_count = %d, want 1", got)
	}
	if err := e.payments.CancelPayment(ctx, u.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 0 {
		t.Fatalf("used_count after cancel = %d, want 0 (released)", got)
	}
	// Cancelling twice must not release twice.
	_ = e.payments.CancelPayment(ctx, u.ID, res.PaymentID)
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 0 {
		t.Fatalf("used_count after double cancel = %d, want 0", got)
	}
}

func TestCoupon_LatePaymentAfterCancelStillCountsTheUse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	c := e.coupon(t, 5, 50)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, c.Code)
	_ = e.payments.CancelPayment(ctx, u.ID, res.PaymentID) // user backs out...
	e.bkash.pay(res.PaymentID)                             // ...but actually paid

	if _, err := e.payments.ExecutePayment(ctx, u.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 1 {
		t.Fatalf("used_count = %d, want 1", got)
	}
	if !reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("paid user was not granted Pro")
	}
}

// ─── reconciliation ──────────────────────────────────────────────────────

func TestReconcile_RecoversPaymentTheAppNeverExecuted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")

	e.bkash.pay(res.PaymentID) // user paid, then the app died: execute never called
	e.backdate(t, "bkash_transactions", mustTxID(t, e, res.PaymentID), 10*time.Minute)

	stats, err := e.payments.ReconcilePending(ctx, 2*time.Minute, time.Hour, 50)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Recovered != 1 || stats.Errors != 0 {
		t.Fatalf("stats = %+v, want 1 recovered", stats)
	}
	if !reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("recovered payment did not grant Pro")
	}
	if n := count(t, e.db, &domain.Invoice{}, ""); n != 1 {
		t.Fatalf("invoices = %d, want 1", n)
	}
	assertLedgerBalanced(t, e.db)

	// A second pass (or another instance) must not grant again.
	if _, err := e.payments.ReconcilePending(ctx, 2*time.Minute, time.Hour, 50); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, &domain.UserSubscription{}, ""); n != 1 {
		t.Fatalf("subscriptions after 2nd pass = %d, want 1", n)
	}
}

func TestReconcile_ClosesAbandonedCheckoutAndReleasesCoupon(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	c := e.coupon(t, 1, 50)
	old, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, c.Code)
	e.backdate(t, "bkash_transactions", mustTxID(t, e, old.PaymentID), 3*time.Hour)

	fresh, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")
	e.backdate(t, "bkash_transactions", mustTxID(t, e, fresh.PaymentID), 10*time.Minute)

	stats, err := e.payments.ReconcilePending(ctx, 2*time.Minute, time.Hour, 50)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Cancelled != 1 {
		t.Fatalf("cancelled = %d, want 1 (only the 3h-old one)", stats.Cancelled)
	}
	var tx domain.BkashTransaction
	e.db.First(&tx, "payment_id = ?", fresh.PaymentID)
	if tx.Status != domain.BkashStatusInitiated {
		t.Fatalf("10-minute-old checkout is %s, want still initiated", tx.Status)
	}
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 0 {
		t.Fatalf("coupon used_count = %d, want 0 (released)", got)
	}
}

func TestReconcile_BkashUnreachableLeavesPaymentPending(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")
	e.bkash.pay(res.PaymentID)
	e.backdate(t, "bkash_transactions", mustTxID(t, e, res.PaymentID), 5*time.Hour)
	e.bkash.queryErr = errors.New("bkash down")

	stats, _ := e.payments.ReconcilePending(ctx, 2*time.Minute, time.Hour, 50)
	if stats.Errors != 1 || stats.Cancelled != 0 {
		t.Fatalf("stats = %+v: must not cancel a payment it couldn't verify", stats)
	}
	var tx domain.BkashTransaction
	e.db.First(&tx, "payment_id = ?", res.PaymentID)
	if tx.Status != domain.BkashStatusInitiated {
		t.Fatalf("status = %s, want initiated", tx.Status)
	}

	// bKash comes back: the next pass recovers the payment.
	e.bkash.queryErr = nil
	stats, _ = e.payments.ReconcilePending(ctx, 2*time.Minute, time.Hour, 50)
	if stats.Recovered != 1 {
		t.Fatalf("stats = %+v, want recovered", stats)
	}
}

// ─── admin grant ─────────────────────────────────────────────────────────

func TestAdminGrantPro_IsFreeAndStacks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	admin := e.user(t)
	plan := e.plan(t, 100, 30, false)

	sub, err := e.payments.AdminGrantPro(ctx, admin.ID, u.ID, plan.ID, "scholarship")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Price != 0 || sub.GrantReason != "admin_grant" || sub.GrantedBy == nil {
		t.Fatalf("grant = %+v", sub)
	}
	if !reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("user not pro after grant")
	}
	// Free grants book no revenue.
	if n := count(t, e.db, &domain.Journal{}, ""); n != 0 {
		t.Fatalf("journals = %d, want 0", n)
	}
}
