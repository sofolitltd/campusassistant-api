package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

type sentNote struct {
	users []uuid.UUID
	n     domain.Notification
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sentNote
}

func (f *fakeSender) SendToUsers(_ context.Context, n domain.Notification, users []uuid.UUID, _ uuid.UUID, _ ...onCommitted) (*domain.Notification, []domain.NotificationRecipient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentNote{users: users, n: n})
	return &n, nil, nil
}

func (f *fakeSender) to(u uuid.UUID) []domain.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Notification
	for _, s := range f.sent {
		for _, id := range s.users {
			if id == u {
				out = append(out, s.n)
			}
		}
	}
	return out
}

func newOrderSvc(e *env) (*OrderService, *fakeSender) {
	fs := &fakeSender{}
	n := NewOrderNotifier(e.db, fs)
	e.billing.SetOrderNotifier(n)
	e.orders.SetOrderNotifier(n)
	return NewOrderService(e.db, e.billing, n), fs
}

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to domain.OrderStatus
		ok       bool
	}{
		{domain.OrderStatusPendingPayment, domain.OrderStatusPaid, true},
		{domain.OrderStatusProcessing, domain.OrderStatusDelivered, true}, // skipping is fine
		{domain.OrderStatusShipped, domain.OrderStatusProcessing, false},  // never backwards
		{domain.OrderStatusDelivered, domain.OrderStatusCancelled, false}, // final
		{domain.OrderStatusCancelled, domain.OrderStatusPaid, false},      // final
		{domain.OrderStatusShipped, domain.OrderStatusCancelled, true},
		{domain.OrderStatusPaid, domain.OrderStatusPaid, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("%s→%s = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestTransition_RecordsEventsAndNotifiesBuyer(t *testing.T) {
	e := newEnv(t)
	svc, fs := newOrderSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})

	for _, st := range []domain.OrderStatus{domain.OrderStatusShipped, domain.OrderStatusDelivered} {
		if err := svc.Transition(ctx, o.ID, st, uuid.Nil); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}
	if got := reload[domain.Order](t, e.db, o.ID).Status; got != domain.OrderStatusDelivered {
		t.Fatalf("status = %s", got)
	}
	if n := count(t, e.db, &domain.OrderEvent{}, "order_id = ?", o.ID); n != 2 {
		t.Fatalf("events = %d, want 2", n)
	}
	if n := len(fs.to(buyer.ID)); n != 2 {
		t.Fatalf("buyer notifications = %d, want 2", n)
	}
	// Delivery booked the merchant's earnings.
	if n := count(t, e.db, &domain.Journal{}, "kind = ?", KindOrderRelease); n != 1 {
		t.Fatalf("release journals = %d, want 1", n)
	}
	assertLedgerBalanced(t, e.db)
}

func TestTransition_IsIdempotentAndRejectsInvalid(t *testing.T) {
	e := newEnv(t)
	svc, fs := newOrderSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})

	if err := svc.Transition(ctx, o.ID, domain.OrderStatusShipped, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.Transition(ctx, o.ID, domain.OrderStatusShipped, uuid.Nil); err != nil {
		t.Fatalf("repeat should be a no-op, got %v", err)
	}
	if n := count(t, e.db, &domain.OrderEvent{}, "order_id = ?", o.ID); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	if n := len(fs.to(buyer.ID)); n != 1 {
		t.Fatalf("buyer notified %d times, want 1", n)
	}
	if err := svc.Transition(ctx, o.ID, domain.OrderStatusProcessing, uuid.Nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("backwards move: got %v", err)
	}
}

func TestTransition_MutedBuyerGetsNothing(t *testing.T) {
	e := newEnv(t)
	svc, fs := newOrderSvc(e)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})
	if err := e.db.Create(&domain.NotificationMute{UserID: buyer.ID, Category: "marketplace"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.Transition(context.Background(), o.ID, domain.OrderStatusShipped, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if n := len(fs.to(buyer.ID)); n != 0 {
		t.Fatalf("muted buyer got %d notifications", n)
	}
}

func TestOnCreated_NotifiesSellersOnlyForCOD(t *testing.T) {
	e := newEnv(t)
	svc, fs := newOrderSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)

	cod := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 2})
	online := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 100, 1})
	for _, o := range []domain.Order{cod, online} {
		o := o
		if err := svc.OnCreated(ctx, &o); err != nil {
			t.Fatal(err)
		}
	}
	got := fs.to(m.UserID)
	if len(got) != 1 || got[0].Title != "New order received" {
		t.Fatalf("seller notifications = %+v, want exactly one new-order alert", got)
	}
}

func TestPaidBkashOrder_RecordsEventAndAlertsBothSides(t *testing.T) {
	e := newEnv(t)
	_, fs := newOrderSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 250, 1})

	res, err := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.bkash.pay(res.PaymentID)
	if err := e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, &domain.OrderEvent{}, "order_id = ? AND status = ?", o.ID, domain.OrderStatusPaid); n != 1 {
		t.Fatalf("paid events = %d, want 1", n)
	}
	if len(fs.to(buyer.ID)) != 1 || len(fs.to(m.UserID)) != 1 {
		t.Fatalf("want one alert each: buyer=%d seller=%d", len(fs.to(buyer.ID)), len(fs.to(m.UserID)))
	}
	// A repeated execute must not duplicate the event or the alerts.
	if err := e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, &domain.OrderEvent{}, "order_id = ?", o.ID); n != 1 {
		t.Fatalf("events after retry = %d, want 1", n)
	}
	if len(fs.to(buyer.ID)) != 1 {
		t.Fatalf("buyer alerted %d times", len(fs.to(buyer.ID)))
	}
}

func TestCancelByBuyer(t *testing.T) {
	e := newEnv(t)
	svc, fs := newOrderSvc(e)
	ctx := context.Background()
	buyer, other := e.user(t), e.user(t)
	m := e.merchant(t, 10, false)

	unpaid := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 100, 1})
	if err := svc.CancelByBuyer(ctx, other.ID, unpaid.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other user: got %v", err)
	}
	if err := svc.CancelByBuyer(ctx, buyer.ID, unpaid.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelByBuyer(ctx, buyer.ID, unpaid.ID); err != nil {
		t.Fatalf("second cancel should be a no-op: %v", err)
	}

	paid := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPaid, orderLine{m, 100, 1})
	if err := svc.CancelByBuyer(ctx, buyer.ID, paid.ID); !errors.Is(err, ErrOrderNotCancellable) {
		t.Fatalf("paid bKash order must go through refund, got %v", err)
	}

	cod := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})
	if err := svc.CancelByBuyer(ctx, buyer.ID, cod.ID); err != nil {
		t.Fatal(err)
	}
	if len(fs.to(m.UserID)) != 1 {
		t.Fatalf("seller should hear about the COD cancellation, got %d", len(fs.to(m.UserID)))
	}

	shipped := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusShipped, orderLine{m, 100, 1})
	if err := svc.CancelByBuyer(ctx, buyer.ID, shipped.ID); !errors.Is(err, ErrOrderNotCancellable) {
		t.Fatalf("shipped order: got %v", err)
	}
}

func TestRefundOrder_RecordsCancelledEvent(t *testing.T) {
	e := newEnv(t)
	_, fs := newOrderSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	o := e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 300, 1})
	res, _ := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
	e.bkash.pay(res.PaymentID)
	if err := e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID); err != nil {
		t.Fatal(err)
	}
	before := len(fs.to(buyer.ID))
	if _, err := e.billing.RefundOrder(ctx, uuid.New(), o.ID, "REF-1", "changed mind"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, &domain.OrderEvent{}, "order_id = ? AND status = ?", o.ID, domain.OrderStatusCancelled); n != 1 {
		t.Fatalf("cancelled events = %d, want 1", n)
	}
	if len(fs.to(buyer.ID)) != before+1 {
		t.Fatalf("buyer should be told about the refund cancellation")
	}
}
