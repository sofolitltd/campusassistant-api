package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func (e *env) stockOf(t *testing.T, id uuid.UUID) int {
	t.Helper()
	return reload[domain.Product](t, e.db, id).Stock
}

// pending builds (but does not save) an order for qty of p.
func pendingOrder(buyer domain.User, m domain.Merchant, p domain.Product, qty int, method domain.PaymentMethod) *domain.Order {
	status := domain.OrderStatusPendingPayment
	if method == domain.PaymentMethodCashOnDelivery {
		status = domain.OrderStatusProcessing
	}
	return &domain.Order{BuyerID: buyer.ID, ShippingRecipientName: "R", ShippingPhone: "01", ShippingAddressLine: "A", ShippingCity: "C",
		Status: status, PaymentMethod: method, TotalAmount: p.Price * qty,
		Items: []domain.OrderItem{{ProductID: p.ID, ProductTitle: p.Title, MerchantID: m.ID, Quantity: qty, UnitPrice: p.Price}}}
}

func TestPlaceOrder_ReservesStockAndRecordsEvent(t *testing.T) {
	e := newEnv(t)
	svc, fs := newOrderSvc(e)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 5)

	o := pendingOrder(buyer, m, p, 2, domain.PaymentMethodCashOnDelivery)
	if err := svc.PlaceOrder(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := e.stockOf(t, p.ID); got != 3 {
		t.Fatalf("stock = %d, want 3", got)
	}
	if n := count(t, e.db, &domain.OrderEvent{}, "order_id = ?", o.ID); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	if len(fs.to(m.UserID)) != 1 {
		t.Fatalf("seller should be alerted to a live COD order")
	}
}

func TestPlaceOrder_RejectsOversellAndRollsBackEverything(t *testing.T) {
	e := newEnv(t)
	svc, _ := newOrderSvc(e)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	plenty, scarce := e.product(t, m, 100, 10), e.product(t, m, 50, 1)

	o := pendingOrder(buyer, m, plenty, 3, domain.PaymentMethodCashOnDelivery)
	o.Items = append(o.Items, domain.OrderItem{ProductID: scarce.ID, ProductTitle: scarce.Title, MerchantID: m.ID, Quantity: 2, UnitPrice: 50})

	err := svc.PlaceOrder(context.Background(), o)
	var se *StockError
	if !errors.As(err, &se) || se.Available != 1 {
		t.Fatalf("want StockError with 1 left, got %v", err)
	}
	if e.stockOf(t, plenty.ID) != 10 || e.stockOf(t, scarce.ID) != 1 {
		t.Fatalf("a failed checkout must not hold any stock: %d / %d", e.stockOf(t, plenty.ID), e.stockOf(t, scarce.ID))
	}
	if n := count(t, e.db, &domain.Order{}, ""); n != 0 {
		t.Fatalf("no order should exist, got %d", n)
	}
}

func TestPlaceOrder_UnpublishedAndSoldOut(t *testing.T) {
	e := newEnv(t)
	svc, _ := newOrderSvc(e)
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	hidden := e.product(t, m, 100, 5)
	if err := e.db.Model(&domain.Product{}).Where("id = ?", hidden.ID).Update("is_published", false).Error; err != nil {
		t.Fatal(err)
	}
	soldOut := e.product(t, m, 100, 0)

	var se *StockError
	if err := svc.PlaceOrder(context.Background(), pendingOrder(buyer, m, hidden, 1, domain.PaymentMethodBkash)); !errors.As(err, &se) || !se.Gone {
		t.Fatalf("unpublished: got %v", err)
	}
	if err := svc.PlaceOrder(context.Background(), pendingOrder(buyer, m, soldOut, 1, domain.PaymentMethodBkash)); !errors.As(err, &se) || se.Available != 0 || se.Gone {
		t.Fatalf("sold out: got %v", err)
	}
}

func TestPlaceOrder_LastUnitGoesToExactlyOneBuyer(t *testing.T) {
	e := newEnv(t)
	svc, _ := newOrderSvc(e)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 1)
	buyers := []domain.User{e.user(t), e.user(t), e.user(t), e.user(t)}

	errs := runConcurrently(len(buyers), func(i int) error {
		return svc.PlaceOrder(context.Background(), pendingOrder(buyers[i], m, p, 1, domain.PaymentMethodCashOnDelivery))
	})
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d buyers got the last unit, want 1 (errs: %v)", won, errs)
	}
	if e.stockOf(t, p.ID) != 0 {
		t.Fatalf("stock = %d, want 0", e.stockOf(t, p.ID))
	}
}

func TestCancellationReturnsStock(t *testing.T) {
	ctx := context.Background()
	for name, cancel := range map[string]func(*OrderService, domain.User, uuid.UUID) error{
		"buyer cancel": func(s *OrderService, b domain.User, id uuid.UUID) error { return s.CancelByBuyer(ctx, b.ID, id) },
		"admin cancel": func(s *OrderService, _ domain.User, id uuid.UUID) error {
			return s.Transition(ctx, id, domain.OrderStatusCancelled, uuid.New())
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			svc, _ := newOrderSvc(e)
			buyer := e.user(t)
			m := e.merchant(t, 10, false)
			p := e.product(t, m, 100, 5)
			o := pendingOrder(buyer, m, p, 2, domain.PaymentMethodCashOnDelivery)
			if err := svc.PlaceOrder(ctx, o); err != nil {
				t.Fatal(err)
			}
			if err := cancel(svc, buyer, o.ID); err != nil {
				t.Fatal(err)
			}
			if got := e.stockOf(t, p.ID); got != 5 {
				t.Fatalf("stock after cancel = %d, want 5", got)
			}
			// Cancelling again must not restock twice.
			_ = cancel(svc, buyer, o.ID)
			if got := e.stockOf(t, p.ID); got != 5 {
				t.Fatalf("stock after repeat cancel = %d, want 5", got)
			}
		})
	}
}

func TestRefund_RestocksUndeliveredButNotDelivered(t *testing.T) {
	ctx := context.Background()
	for _, deliver := range []bool{false, true} {
		e := newEnv(t)
		svc, _ := newOrderSvc(e)
		buyer := e.user(t)
		m := e.merchant(t, 10, false)
		p := e.product(t, m, 300, 4)
		o := pendingOrder(buyer, m, p, 1, domain.PaymentMethodBkash)
		if err := svc.PlaceOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
		res, _ := e.orders.CreatePayment(ctx, buyer.ID, o.ID)
		e.bkash.pay(res.PaymentID)
		if err := e.orders.ExecutePayment(ctx, buyer.ID, res.PaymentID); err != nil {
			t.Fatal(err)
		}
		if deliver {
			if err := svc.Transition(ctx, o.ID, domain.OrderStatusDelivered, uuid.Nil); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.billing.RefundOrder(ctx, uuid.New(), o.ID, "REF", "test"); err != nil {
			t.Fatal(err)
		}
		want := 4
		if deliver {
			want = 3 // the goods are with the buyer
		}
		if got := e.stockOf(t, p.ID); got != want {
			t.Fatalf("deliver=%v: stock = %d, want %d", deliver, got, want)
		}
	}
}

func TestExpireUnpaid(t *testing.T) {
	e := newEnv(t)
	svc, _ := newOrderSvc(e)
	ctx := context.Background()
	buyer := e.user(t)
	m := e.merchant(t, 10, false)
	p := e.product(t, m, 100, 10)

	place := func(qty int) domain.Order {
		o := pendingOrder(buyer, m, p, qty, domain.PaymentMethodBkash)
		if err := svc.PlaceOrder(ctx, o); err != nil {
			t.Fatal(err)
		}
		return *o
	}
	abandoned := place(1) // never paid, old
	fresh := place(2)     // never paid, but recent
	inFlight := place(3)  // old, but a payment session is still open
	e.backdate(t, "orders", abandoned.ID, 3*time.Hour)
	e.backdate(t, "orders", inFlight.ID, 3*time.Hour)
	if err := e.db.Create(&domain.OrderTransaction{OrderID: inFlight.ID, PaymentID: "PAY-X", Amount: 300, Status: domain.BkashStatusInitiated}).Error; err != nil {
		t.Fatal(err)
	}

	n, err := svc.ExpireUnpaid(ctx, 2*time.Hour, 50)
	if err != nil || n != 1 {
		t.Fatalf("expired %d (err %v), want 1", n, err)
	}
	if got := reload[domain.Order](t, e.db, abandoned.ID).Status; got != domain.OrderStatusCancelled {
		t.Fatalf("abandoned order = %s", got)
	}
	if reload[domain.Order](t, e.db, fresh.ID).Status != domain.OrderStatusPendingPayment ||
		reload[domain.Order](t, e.db, inFlight.ID).Status != domain.OrderStatusPendingPayment {
		t.Fatalf("recent and in-flight orders must be left alone")
	}
	if got := e.stockOf(t, p.ID); got != 10-1-2-3+1 { // only the abandoned order's unit came back
		t.Fatalf("stock = %d, want %d", got, 10-1-2-3+1)
	}
}
