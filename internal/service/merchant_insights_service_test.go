package service

import (
	"context"
	"testing"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
)

func TestStats_CountsOnlyLiveOrdersAndComputesNet(t *testing.T) {
	e := newEnv(t)
	ins := NewInsightsService(e.db)
	buyer := e.user(t)
	m := e.merchant(t, 10, false) // 10% commission
	other := e.merchant(t, 10, false)

	e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusDelivered, orderLine{m, 100, 2}) // 200
	e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPaid, orderLine{m, 300, 1})              // 300
	e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusPendingPayment, orderLine{m, 999, 1})    // not a sale
	e.order(t, buyer, domain.PaymentMethodBkash, domain.OrderStatusCancelled, orderLine{m, 999, 1})         // not a sale
	e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusDelivered, orderLine{other, 777, 1})

	s, err := ins.Stats(context.Background(), m.ID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if s.Orders != 2 || s.Delivered != 1 || s.Units != 3 || s.GrossRevenue != 500 {
		t.Fatalf("stats = orders %d delivered %d units %d gross %d", s.Orders, s.Delivered, s.Units, s.GrossRevenue)
	}
	if s.NetRevenue != 450 || s.AvgOrder != 250 {
		t.Fatalf("net %d avg %d, want 450 / 250", s.NetRevenue, s.AvgOrder)
	}
	if len(s.Daily) != 30 {
		t.Fatalf("daily points = %d, want 30 (zero-filled)", len(s.Daily))
	}
	today := s.Daily[len(s.Daily)-1]
	if today.Orders != 2 || today.Revenue != 500 {
		t.Fatalf("today = %+v", today)
	}
}

func TestStats_TopProductsAndViews(t *testing.T) {
	e := newEnv(t)
	ins := NewInsightsService(e.db)
	buyer := e.user(t)
	m := e.merchant(t, 0, false)
	p1, p2 := e.product(t, m, 100, 10), e.product(t, m, 500, 2) // p2 is low stock
	for _, p := range []domain.Product{p1, p2} {
		o := e.deliveredOrderFor(t, buyer, m, p)
		_ = o
	}
	if err := e.db.Exec("UPDATE products SET view_count = 40 WHERE id = ?", p2.ID).Error; err != nil {
		t.Fatal(err)
	}
	s, err := ins.Stats(context.Background(), m.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.TopProducts) != 2 || s.TopProducts[0].ProductID != p2.ID || s.TopProducts[0].Views != 40 {
		t.Fatalf("top products = %+v", s.TopProducts)
	}
	if s.Views != 40 || s.LowStock != 1 {
		t.Fatalf("views %d lowStock %d, want 40 / 1", s.Views, s.LowStock)
	}
}

func TestShipSpeed_AveragesLiveToShipped(t *testing.T) {
	e := newEnv(t)
	ins := NewInsightsService(e.db)
	buyer := e.user(t)
	m := e.merchant(t, 0, false)
	now := time.Now()

	mk := func(liveAgo, shippedAgo time.Duration) {
		o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusShipped, orderLine{m, 100, 1})
		for _, ev := range []struct {
			st domain.OrderStatus
			at time.Time
		}{{domain.OrderStatusProcessing, now.Add(-liveAgo)}, {domain.OrderStatusShipped, now.Add(-shippedAgo)}} {
			if err := e.db.Create(&domain.OrderEvent{OrderID: o.ID, Status: ev.st, ActorID: uuid.Nil, Base: domain.Base{CreatedAt: ev.at}}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	mk(30*time.Hour, 24*time.Hour) // 6h
	mk(20*time.Hour, 10*time.Hour) // 10h

	if err := ins.RecordShipSpeed(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	got := reload[domain.Merchant](t, e.db, m.ID)
	if got.ShippedCount != 2 || got.AvgShipHours < 7.9 || got.AvgShipHours > 8.1 {
		t.Fatalf("ship speed = %.2fh over %d orders, want ~8h over 2", got.AvgShipHours, got.ShippedCount)
	}
}

func TestShipSpeed_UpdatedWhenOrderShips(t *testing.T) {
	e := newEnv(t)
	svc, _ := newOrderSvc(e)
	svc.SetInsights(NewInsightsService(e.db))
	buyer := e.user(t)
	m := e.merchant(t, 0, false)
	o := e.order(t, buyer, domain.PaymentMethodCashOnDelivery, domain.OrderStatusProcessing, orderLine{m, 100, 1})
	if err := svc.OnCreated(context.Background(), &o); err != nil {
		t.Fatal(err)
	}
	if err := svc.Transition(context.Background(), o.ID, domain.OrderStatusShipped, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if got := reload[domain.Merchant](t, e.db, m.ID); got.ShippedCount != 1 {
		t.Fatalf("shipped_count = %d, want 1", got.ShippedCount)
	}
}
