package service

import (
	"context"
	"errors"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func paidSubscription(t *testing.T, e *env, u domain.User, plan domain.SubscriptionPlan, coupon string) domain.BkashTransaction {
	t.Helper()
	res, err := e.payments.CreatePayment(context.Background(), u.ID, plan.ID, coupon)
	if err != nil {
		t.Fatal(err)
	}
	e.bkash.pay(res.PaymentID)
	if _, err := e.payments.ExecutePayment(context.Background(), u.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	var bt domain.BkashTransaction
	e.db.First(&bt, "payment_id = ?", res.PaymentID)
	return bt
}

func TestRefundSubscription_RevertsRevenueProAndInvoice(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, admin := e.user(t), uuid.New()
	plan := e.plan(t, 100, 30, false)
	c := e.coupon(t, 3, 50)
	bt := paidSubscription(t, e, u, plan, c.Code)

	r, err := e.billing.RefundSubscriptionPayment(ctx, admin, bt.ID, "RFND1", "duplicate")
	if err != nil || r.Amount != 50 {
		t.Fatalf("refund=%+v err=%v", r, err)
	}
	if reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("user is still Pro after refund")
	}
	sum, _ := e.billing.Summary(ctx)
	if sum.SubscriptionRevenue != 0 || sum.GatewayHeld != 0 {
		t.Fatalf("summary = %+v, want revenue and gateway back to 0", sum)
	}
	var inv domain.Invoice
	e.db.First(&inv)
	if inv.VoidedAt == nil {
		t.Fatal("invoice not voided")
	}
	if got := reload[domain.CouponCode](t, e.db, c.ID).UsedCount; got != 0 {
		t.Fatalf("coupon used_count = %d, want 0 (use returned)", got)
	}
	if got := reload[domain.BkashTransaction](t, e.db, bt.ID).Status; got != domain.BkashStatusRefunded {
		t.Fatalf("status = %s", got)
	}
	assertLedgerBalanced(t, e.db)

	if _, err := e.billing.RefundSubscriptionPayment(ctx, admin, bt.ID, "RFND2", ""); !errors.Is(err, ErrAlreadyRefunded) {
		t.Fatalf("second refund: err = %v, want ErrAlreadyRefunded", err)
	}
	// Refunded subscriptions must not count toward entitlements either.
	if a, _ := NewEntitlementService(e.db).Check(ctx, u.ID, domain.FeatureAdFree); a.Allowed {
		t.Fatal("refunded user still entitled")
	}
}

func TestRefundSubscription_KeepsProIfAnotherPurchaseIsStillRunning(t *testing.T) {
	e := newEnv(t)
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	first := paidSubscription(t, e, u, plan, "")
	paidSubscription(t, e, u, plan, "") // stacked on top

	if _, err := e.billing.RefundSubscriptionPayment(context.Background(), uuid.New(), first.ID, "R", ""); err != nil {
		t.Fatal(err)
	}
	if !reload[domain.User](t, e.db, u.ID).IsPro {
		t.Fatal("refunding one purchase must not remove Pro earned by another")
	}
}

func TestRefundSubscription_NeedsReviewIsAPureStatusChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "")
	e.bkash.pay(res.PaymentID)
	e.bkash.amountOverride = "5"
	e.payments.ExecutePayment(ctx, u.ID, res.PaymentID) // flagged
	var bt domain.BkashTransaction
	e.db.First(&bt, "payment_id = ?", res.PaymentID)

	if _, err := e.billing.RefundSubscriptionPayment(ctx, uuid.New(), bt.ID, "R", "mismatch"); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.BkashTransaction](t, e.db, bt.ID).Status; got != domain.BkashStatusRefunded {
		t.Fatalf("status = %s, want refunded", got)
	}
	if n := count(t, e.db, &domain.Journal{}, ""); n != 0 {
		t.Fatalf("journals = %d, want 0", n)
	}
}

func TestRefundSubscription_RequiresReferenceAndCompletedPayment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, u.ID, plan.ID, "") // still initiated
	var bt domain.BkashTransaction
	e.db.First(&bt, "payment_id = ?", res.PaymentID)

	if _, err := e.billing.RefundSubscriptionPayment(ctx, uuid.New(), bt.ID, "  ", ""); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("blank reference: err = %v", err)
	}
	if _, err := e.billing.RefundSubscriptionPayment(ctx, uuid.New(), bt.ID, "R", ""); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("initiated payment: err = %v, want ErrNotRefundable", err)
	}
}

func TestRefundOrder_UndeliveredReversesPaymentOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := paidBkashOrder(t, e, buyer, orderLine{m, 1000, 1})

	if _, err := e.billing.RefundOrder(ctx, uuid.New(), o.ID, "R1", "changed mind"); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.Order](t, e.db, o.ID).Status; got != domain.OrderStatusCancelled {
		t.Fatalf("order = %s, want cancelled", got)
	}
	sum, _ := e.billing.Summary(ctx)
	if sum.OrdersEscrow != 0 || sum.GatewayHeld != 0 {
		t.Fatalf("summary = %+v, want escrow and gateway back to 0", sum)
	}
	assertLedgerBalanced(t, e.db)
	if _, err := e.billing.RefundOrder(ctx, uuid.New(), o.ID, "R2", ""); !errors.Is(err, ErrAlreadyRefunded) {
		t.Fatalf("second refund: err = %v", err)
	}
}

func TestRefundOrder_DeliveredReversesMerchantEarningsAndCommission(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := paidBkashOrder(t, e, buyer, orderLine{m, 1000, 1})
	e.deliver(t, o.ID)
	if b := balance(t, e, m); b != 900 {
		t.Fatalf("pre-refund balance = %d", b)
	}

	if _, err := e.billing.RefundOrder(ctx, uuid.New(), o.ID, "R1", ""); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, e, m); b != 0 {
		t.Fatalf("merchant balance = %d, want 0 after refund", b)
	}
	sum, _ := e.billing.Summary(ctx)
	if sum.CommissionRevenue != 0 || sum.GatewayHeld != 0 {
		t.Fatalf("summary = %+v, want commission and gateway back to 0", sum)
	}
	assertLedgerBalanced(t, e.db)
}

func TestRefundOrder_CashOnDeliveryIsNotRefundable(t *testing.T) {
	e := newEnv(t)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})
	if _, err := e.billing.RefundOrder(context.Background(), uuid.New(), o.ID, "R", ""); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("err = %v, want ErrNotRefundable", err)
	}
}

func TestListPayments_FiltersByStatusAcrossBothKinds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	paidSubscription(t, e, u, plan, "")
	m := e.merchant(t, 10, false)
	paidBkashOrder(t, e, e.user(t), orderLine{m, 100, 1})
	e.payments.CreatePayment(ctx, u.ID, plan.ID, "") // one initiated

	all, err := e.billing.ListPayments(ctx, "", 50, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("all=%d err=%v, want 3", len(all), err)
	}
	done, _ := e.billing.ListPayments(ctx, "completed", 50, 0)
	if len(done) != 2 || done[0].UserEmail == "" {
		t.Fatalf("completed=%+v", done)
	}
	if page, _ := e.billing.ListPayments(ctx, "", 1, 1); len(page) != 1 {
		t.Fatalf("pagination returned %d rows, want 1", len(page))
	}
}

func TestListSubscriptionAttemptsIncludesContactAndUnresolvedStatuses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user(t)
	u.FirstName, u.LastName, u.Phone = "Sam", "User", "+8801700000000"
	if err := e.db.Save(&u).Error; err != nil {
		t.Fatal(err)
	}
	plan := e.plan(t, 100, 30, false)

	for _, status := range []domain.BkashTransactionStatus{
		domain.BkashStatusFailed,
		domain.BkashStatusCancelled,
		domain.BkashStatusNeedsReview,
		domain.BkashStatusInitiated,
		domain.BkashStatusCompleted,
	} {
		if err := e.db.Create(&domain.BkashTransaction{
			UserID: u.ID, PlanID: plan.ID, PaymentID: uuid.NewString(),
			Amount: 100, Status: status,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	rows, total, err := e.billing.ListSubscriptionAttempts(ctx, "unresolved", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("unresolved attempts = %d (count %d), want 3", len(rows), total)
	}
	for _, row := range rows {
		if row.UserEmail != u.Email || row.FirstName != u.FirstName || row.LastName != u.LastName || row.Phone != u.Phone {
			t.Fatalf("attempt contact details = %+v, want the user's current contact details", row)
		}
	}

	rows, total, err = e.billing.ListSubscriptionAttempts(ctx, "failed", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Status != string(domain.BkashStatusFailed) {
		t.Fatalf("failed filter = %+v, count %d; want one failed attempt", rows, total)
	}
}
