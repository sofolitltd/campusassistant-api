package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

// paidBkashOrder creates a bKash order and drives it through checkout.
func paidBkashOrder(t *testing.T, e *env, buyer domain.User, lines ...orderLine) domain.Order {
	t.Helper()
	ctx := context.Background()
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, lines...)
	res, err := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.bkash.pay(res.PaymentID)
	if err := e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	return o
}

func balance(t *testing.T, e *env, m domain.Merchant) int {
	t.Helper()
	b, err := e.billing.MerchantBalance(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ─── marketplace payment ─────────────────────────────────────────────────

func TestOrderPayment_ConcurrentExecutesPayOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 1000, 2})
	res, err := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.bkash.pay(res.PaymentID)

	for i, err := range runConcurrently(6, func(int) error { return e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID) }) {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := reload[domain.Order](t, e.db, o.ID).Status; got != domain.OrderStatusPaid {
		t.Fatalf("order status = %s, want paid", got)
	}
	if n := count(t, e.db, &domain.Journal{}, "kind = ?", KindOrderPayment); n != 1 {
		t.Fatalf("payment journals = %d, want 1", n)
	}
	if n := count(t, e.db, &domain.Invoice{}, ""); n != 1 {
		t.Fatalf("invoices = %d, want 1", n)
	}
	assertLedgerBalanced(t, e.db)
}

func TestOrderPayment_OnlyUnpaidBkashOrdersCanBePaid(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)

	cod := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})
	if _, err := e.orders.CreatePayment(ctx, buyer.ID, cod.ID); !errors.Is(err, ErrOrderNotPayable) {
		t.Fatalf("COD order: err = %v, want ErrOrderNotPayable", err)
	}
	paid := paidBkashOrder(t, e, buyer, orderLine{m, 100, 1})
	if _, err := e.orders.CreatePayment(ctx, buyer.ID, paid.ID); !errors.Is(err, ErrOrderNotPayable) {
		t.Fatalf("already-paid order: err = %v, want ErrOrderNotPayable (would double-charge)", err)
	}
	other := e.user(t)
	fresh := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 100, 1})
	if _, err := e.orders.CreatePayment(ctx, other.ID, fresh.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's order: err = %v, want ErrForbidden", err)
	}
}

func TestOrderPayment_PaidButOrderCancelledIsFlaggedNotResurrected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 500, 1})
	res, _ := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
	e.db.Model(&domain.Order{}).Where("id = ?", o.ID).Update("status", domain.OrderStatusCancelled) // admin cancels mid-checkout
	e.bkash.pay(res.PaymentID)

	if err := e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID); !errors.Is(err, ErrOrderNotPayable) {
		t.Fatalf("err = %v, want ErrOrderNotPayable", err)
	}
	if got := reload[domain.Order](t, e.db, o.ID).Status; got != domain.OrderStatusCancelled {
		t.Fatalf("order status = %s, want it to stay cancelled", got)
	}
	var tx domain.OrderTransaction
	e.db.First(&tx, "payment_id = ?", res.PaymentID)
	if tx.Status != domain.BkashStatusNeedsReview {
		t.Fatalf("payment status = %s, want needs_review so an admin refunds it", tx.Status)
	}
	if n := count(t, e.db, &domain.Journal{}, ""); n != 0 {
		t.Fatalf("journals = %d, want 0 for an unfulfilled payment", n)
	}
}

func TestOrderPayment_ReconcileRecoversUnexecutedPayment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 500, 1})
	res, _ := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
	e.bkash.pay(res.PaymentID) // paid, app never called execute
	var tx domain.OrderTransaction
	e.db.First(&tx, "payment_id = ?", res.PaymentID)
	e.backdate(t, "order_transactions", tx.ID, 10*time.Minute)

	stats, err := e.orders.ReconcilePending(ctx, 2*time.Minute, time.Hour, 50)
	if err != nil || stats.Recovered != 1 {
		t.Fatalf("stats=%+v err=%v, want 1 recovered", stats, err)
	}
	if got := reload[domain.Order](t, e.db, o.ID).Status; got != domain.OrderStatusPaid {
		t.Fatalf("order status = %s, want paid", got)
	}
}

// ─── ledger: delivery → merchant earnings ────────────────────────────────

func TestDelivery_BkashOrderCreditsMerchantNetOfCommission(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := paidBkashOrder(t, e, buyer, orderLine{m, 1000, 2}) // 2000 gross

	// Paid but not delivered: money sits in escrow, merchant has nothing yet.
	if b := balance(t, e, m); b != 0 {
		t.Fatalf("balance before delivery = %d, want 0", b)
	}
	e.deliver(t, o.ID)

	if b := balance(t, e, m); b != 1800 {
		t.Fatalf("merchant balance = %d, want 1800 (2000 - 10%%)", b)
	}
	sum, _ := e.billing.Summary(ctx)
	if sum.CommissionRevenue != 200 || sum.OrdersEscrow != 0 || sum.GatewayHeld != 2000 {
		t.Fatalf("summary = %+v, want commission 200, escrow 0, gateway 2000", sum)
	}

	// Booking the same delivery again must change nothing.
	e.deliver(t, o.ID)
	if b := balance(t, e, m); b != 1800 {
		t.Fatalf("balance after repeat = %d, want still 1800", b)
	}
	assertLedgerBalanced(t, e.db)
}

func TestDelivery_SplitsAcrossMerchantsAndPlatformProducts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	a, b := e.merchant(t, 10, false), e.merchant(t, 20, false)
	own := e.merchant(t, 0, true)
	o := paidBkashOrder(t, e, buyer, orderLine{a, 500, 1}, orderLine{b, 100, 3}, orderLine{own, 250, 1})
	e.deliver(t, o.ID)

	if got := balance(t, e, a); got != 450 {
		t.Fatalf("A = %d, want 450", got)
	}
	if got := balance(t, e, b); got != 240 { // 300 - 60
		t.Fatalf("B = %d, want 240", got)
	}
	if got := balance(t, e, own); got != 0 {
		t.Fatalf("platform merchant accrued %d, want 0 (its sales are platform revenue)", got)
	}
	sum, _ := e.billing.Summary(ctx)
	if sum.CommissionRevenue != 50+60 || sum.PlatformSales != 250 {
		t.Fatalf("summary = %+v, want commission 110, platform sales 250", sum)
	}
	assertLedgerBalanced(t, e.db)
}

func TestDelivery_CashOnDeliveryOnlyBooksCommissionOwed(t *testing.T) {
	e := newEnv(t)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 1000, 1})
	e.deliver(t, o.ID)

	if b := balance(t, e, m); b != -100 {
		t.Fatalf("balance = %d, want -100 (merchant holds the cash, owes 10%%)", b)
	}
	if n := count(t, e.db, &domain.Invoice{}, "source_id = ?", o.ID); n != 1 {
		t.Fatalf("COD invoices = %d, want 1", n)
	}
	assertLedgerBalanced(t, e.db)
}

func TestDelivery_UndeliveredOrderBooksNothing(t *testing.T) {
	e := newEnv(t)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := paidBkashOrder(t, e, buyer, orderLine{m, 1000, 1})
	if err := e.billing.OnOrderDelivered(context.Background(), o.ID); !errors.Is(err, ErrOrderNotDelivered) {
		t.Fatalf("err = %v, want ErrOrderNotDelivered", err)
	}
}

func TestReleaseDeliveredOrders_SweeperCatchesMissedOnes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := paidBkashOrder(t, e, buyer, orderLine{m, 1000, 1})
	// Status flips to delivered but the booking call never happens (crash).
	e.db.Model(&domain.Order{}).Where("id = ?", o.ID).Update("status", domain.OrderStatusDelivered)

	n, err := e.billing.ReleaseDeliveredOrders(ctx, 50)
	if err != nil || n != 1 {
		t.Fatalf("released=%d err=%v, want 1", n, err)
	}
	if b := balance(t, e, m); b != 900 {
		t.Fatalf("balance = %d, want 900", b)
	}
	if n, _ := e.billing.ReleaseDeliveredOrders(ctx, 50); n != 0 {
		t.Fatalf("second sweep released %d, want 0", n)
	}
}

func TestDelivery_PreLedgerPaidOrderIsBackfilledWithoutNegativeEscrow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	// An order that was paid before the ledger existed: paid status, no journal.
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPaid, orderLine{m, 1000, 1})
	e.deliver(t, o.ID)

	sum, _ := e.billing.Summary(ctx)
	if sum.OrdersEscrow != 0 {
		t.Fatalf("escrow = %d, want 0", sum.OrdersEscrow)
	}
	if b := balance(t, e, m); b != 900 {
		t.Fatalf("balance = %d, want 900", b)
	}
	assertLedgerBalanced(t, e.db)
}

// ─── payouts ─────────────────────────────────────────────────────────────

func fundedMerchant(t *testing.T, e *env, net int) domain.Merchant {
	t.Helper()
	m := e.merchant(t, 0, false) // no commission → net == gross
	buyer := e.user(t)
	e.deliver(t, paidBkashOrder(t, e, buyer, orderLine{m, net, 1}).ID)
	return m
}

func TestPayout_RequestPayRejectLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := fundedMerchant(t, e, 2000)
	admin := uuid.New()

	// Guards.
	if _, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 100); !errors.Is(err, ErrBelowMinPayout) {
		t.Fatalf("below min: err = %v", err)
	}
	if _, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 2500); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("overdraw: err = %v", err)
	}

	p, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if b := balance(t, e, m); b != 500 {
		t.Fatalf("balance after request = %d, want 500 (reserved immediately)", b)
	}
	if p.Method != "bkash" || p.Account != "01700000000" {
		t.Fatalf("payout destination not snapshotted: %+v", p)
	}

	paid, err := e.billing.MarkPayoutPaid(ctx, p.ID, admin, "TRX123")
	if err != nil || paid.Status != domain.PayoutPaid || paid.Reference != "TRX123" {
		t.Fatalf("paid=%+v err=%v", paid, err)
	}
	if _, err := e.billing.MarkPayoutPaid(ctx, p.ID, admin, "TRX999"); !errors.Is(err, ErrPayoutNotRequested) {
		t.Fatalf("double pay: err = %v, want ErrPayoutNotRequested", err)
	}
	if _, err := e.billing.RejectPayout(ctx, p.ID, admin, "x"); !errors.Is(err, ErrPayoutNotRequested) {
		t.Fatalf("reject after paid: err = %v", err)
	}
	sum, _ := e.billing.Summary(ctx)
	if sum.PaidOut != 1500 || sum.PayoutsPending != 0 {
		t.Fatalf("summary = %+v, want paid_out 1500, pending 0", sum)
	}

	// A rejected payout returns the money.
	p2, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if b := balance(t, e, m); b != 0 {
		t.Fatalf("balance = %d, want 0", b)
	}
	if _, err := e.billing.RejectPayout(ctx, p2.ID, admin, "wrong account"); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, e, m); b != 500 {
		t.Fatalf("balance after reject = %d, want 500", b)
	}
	assertLedgerBalanced(t, e.db)
}

func TestPayout_ConcurrentRequestsCannotOverdraw(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := fundedMerchant(t, e, 1800)

	errs := runConcurrently(6, func(int) error {
		_, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 1000)
		return err
	})
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInsufficientBalance):
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d payouts of 1000 succeeded against a 1800 balance, want exactly 1", ok)
	}
	if b := balance(t, e, m); b != 800 {
		t.Fatalf("balance = %d, want 800", b)
	}
}

func TestPayout_RequiresApprovedMerchantWithPayoutAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := fundedMerchant(t, e, 2000)

	e.db.Model(&domain.Merchant{}).Where("id = ?", m.ID).Update("payout_account", "")
	if _, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 1000); !errors.Is(err, ErrNoPayoutAccount) {
		t.Fatalf("no account: err = %v", err)
	}
	e.db.Model(&domain.Merchant{}).Where("id = ?", m.ID).Updates(map[string]any{"payout_account": "017", "status": domain.MerchantStatusRejected})
	if _, err := e.billing.RequestPayout(ctx, m.ID, m.UserID, 1000); !errors.Is(err, ErrMerchantNotApproved) {
		t.Fatalf("rejected merchant: err = %v", err)
	}
}

// ─── ledger & invoice primitives ─────────────────────────────────────────

func TestPostJournal_RejectsUnbalancedAndIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	id := uuid.New()
	if _, err := postJournal(db, "k", "src", id, "", []journalLine{{Account: "a", Debit: 10}, {Account: "b", Credit: 9}}); !errors.Is(err, ErrLedgerUnbalanced) {
		t.Fatalf("err = %v, want ErrLedgerUnbalanced", err)
	}
	if n := count(t, db, &domain.Journal{}, ""); n != 0 {
		t.Fatal("unbalanced journal was written")
	}
	lines := []journalLine{{Account: "a", Debit: 10}, {Account: "b", Credit: 10}}
	if created, err := postJournal(db, "k", "src", id, "", lines); err != nil || !created {
		t.Fatalf("first post: created=%v err=%v", created, err)
	}
	if created, err := postJournal(db, "k", "src", id, "", lines); err != nil || created {
		t.Fatalf("repeat post: created=%v err=%v, want created=false", created, err)
	}
	if n := count(t, db, &domain.LedgerEntry{}, ""); n != 2 {
		t.Fatalf("entries = %d, want 2", n)
	}
}

func TestInvoiceNumbers_AreSequentialAndUnique(t *testing.T) {
	e := newEnv(t)
	u := e.user(t)
	plan := e.plan(t, 100, 30, false)
	var numbers []string
	for i := 0; i < 3; i++ {
		res, _ := e.payments.CreatePayment(context.Background(), u.ID, plan.ID, "")
		e.bkash.pay(res.PaymentID)
		if _, err := e.payments.ExecutePayment(context.Background(), u.ID, res.PaymentID); err != nil {
			t.Fatal(err)
		}
	}
	var invs []domain.Invoice
	e.db.Order("number asc").Find(&invs)
	for _, i := range invs {
		numbers = append(numbers, i.Number)
	}
	year := time.Now().UTC().Year()
	want := []string{
		fmt.Sprintf("INV-%d-000001", year), fmt.Sprintf("INV-%d-000002", year), fmt.Sprintf("INV-%d-000003", year),
	}
	if len(numbers) != 3 || numbers[0] != want[0] || numbers[1] != want[1] || numbers[2] != want[2] {
		t.Fatalf("numbers = %v, want %v", numbers, want)
	}
}

func TestInvoice_ReadsAreScopedToOwner(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, other := e.user(t), e.user(t)
	plan := e.plan(t, 100, 30, false)
	res, _ := e.payments.CreatePayment(ctx, owner.ID, plan.ID, "")
	e.bkash.pay(res.PaymentID)
	if _, err := e.payments.ExecutePayment(ctx, owner.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	var inv domain.Invoice
	e.db.First(&inv)

	if _, err := e.billing.GetInvoice(ctx, inv.ID, &owner.ID); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if _, err := e.billing.GetInvoice(ctx, inv.ID, &other.ID); !errors.Is(err, ErrInvoiceNotFound) {
		t.Fatalf("other user: err = %v, want ErrInvoiceNotFound", err)
	}
	html, err := RenderInvoiceHTML(&inv)
	if err != nil || len(html) == 0 {
		t.Fatalf("render: %v", err)
	}
}

func TestInvoiceHTML_EscapesCustomerFields(t *testing.T) {
	inv := &domain.Invoice{Number: "INV-1", CustomerName: `<script>alert(1)</script>`, Currency: "BDT", IssuedAt: time.Now(),
		Lines: []domain.InvoiceLine{{Description: `<img src=x onerror=alert(1)>`, Quantity: 1, UnitPrice: 1, Total: 1}}}
	html, err := RenderInvoiceHTML(inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script>alert(1)", "<img src=x"} {
		if strings.Contains(html, bad) {
			t.Fatalf("unescaped %q in invoice HTML", bad)
		}
	}
}
