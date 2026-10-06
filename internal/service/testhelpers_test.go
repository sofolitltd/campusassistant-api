package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/repository/postgres"
	"campusassistant-api/pkg/bkash"
	applog "campusassistant-api/pkg/logger"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The package logger is a global that must be initialised before use.
func TestMain(m *testing.M) {
	applog.InitLogger("test")
	os.Exit(m.Run())
}

// newTestDB returns an isolated in-memory SQLite database with the billing
// schema. The pool is pinned to one connection so concurrent goroutines
// serialise whole transactions — this exercises the idempotency/guard logic
// (a second fulfilment sees "already completed"), but NOT Postgres row
// locking (SQLite ignores FOR UPDATE). The locking itself is the standard
// SELECT ... FOR UPDATE; see the notes in the PR.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	err = db.AutoMigrate(
		&domain.User{}, &domain.SubscriptionPlan{}, &domain.SubscriptionTarget{}, &domain.UserSubscription{},
		&domain.BkashTransaction{}, &domain.CouponCode{},
		&domain.Order{}, &domain.OrderItem{}, &domain.OrderTransaction{}, &domain.Merchant{},
		&domain.Journal{}, &domain.LedgerEntry{}, &domain.Invoice{}, &domain.InvoiceLine{}, &domain.InvoiceCounter{},
		&domain.MerchantPayout{}, &domain.Refund{}, &domain.PlanEntitlement{}, &domain.UsageCounter{}, &domain.OrderEvent{}, &domain.NotificationMute{},
		&domain.Product{}, &domain.ProductTarget{}, &domain.MarketplaceCategory{}, &domain.ProductReview{}, &domain.WishlistItem{}, &domain.CommissionPolicy{},
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// ─── fake bKash ──────────────────────────────────────────────────────────

type fakePayment struct {
	amount   int
	paid     bool // the user completed payment at bKash
	executed bool // bKash already processed an execute call
}

// fakeBkash mimics the parts of bKash's behaviour the payment logic relies
// on: execute succeeds exactly once for a paid session and is rejected
// afterwards, while the status query always tells the truth.
type fakeBkash struct {
	mu             sync.Mutex
	seq            int
	payments       map[string]*fakePayment
	queryErr       error
	amountOverride string // when set, reported instead of the real amount
	executeCalls   int
}

func newFakeBkash() *fakeBkash { return &fakeBkash{payments: map[string]*fakePayment{}} }

func (f *fakeBkash) IsProduction() bool { return false }

func (f *fakeBkash) CreatePayment(_ context.Context, amount int, _, _, _ string) (*bkash.CreatePaymentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("PAY-%d", f.seq)
	f.payments[id] = &fakePayment{amount: amount}
	return &bkash.CreatePaymentResult{PaymentID: id, BkashURL: "https://bkash.test/" + id}, nil
}

func (f *fakeBkash) amountStr(p *fakePayment) string {
	if f.amountOverride != "" {
		return f.amountOverride
	}
	return strconv.Itoa(p.amount)
}

func (f *fakeBkash) ExecutePayment(_ context.Context, id string) (*bkash.ExecutePaymentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executeCalls++
	p, ok := f.payments[id]
	if !ok {
		return nil, errors.New("unknown payment")
	}
	switch {
	case p.paid && !p.executed:
		p.executed = true
		return &bkash.ExecutePaymentResult{PaymentID: id, TrxID: "TRX-" + id, TransactionStatus: "Completed", Amount: f.amountStr(p), StatusCode: "0000"}, nil
	case p.executed:
		return &bkash.ExecutePaymentResult{PaymentID: id, StatusCode: "2062", StatusMessage: "already executed"}, nil
	default:
		return &bkash.ExecutePaymentResult{PaymentID: id, TransactionStatus: "Initiated", StatusCode: "2056", StatusMessage: "not completed"}, nil
	}
}

func (f *fakeBkash) QueryPayment(_ context.Context, id string) (*bkash.QueryPaymentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	p, ok := f.payments[id]
	if !ok {
		return nil, errors.New("unknown payment")
	}
	status := "Initiated"
	if p.paid {
		status = "Completed"
	}
	return &bkash.QueryPaymentResult{PaymentID: id, TrxID: "TRX-" + id, TransactionStatus: status, Amount: f.amountStr(p), StatusCode: "0000"}, nil
}

// pay simulates the user completing the payment on bKash's page.
func (f *fakeBkash) pay(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payments[id].paid = true
}

// ─── fixtures ────────────────────────────────────────────────────────────

type env struct {
	db        *gorm.DB
	bkash     *fakeBkash
	billing   *BillingService
	payments  *PaymentService
	orders    *MarketplacePaymentService
	orderRepo domain.OrderRepository
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := newTestDB(t)
	fb := newFakeBkash()
	billing := NewBillingService(db)
	orderRepo := postgres.NewOrderRepository(db)
	return &env{
		db: db, bkash: fb, billing: billing, orderRepo: orderRepo,
		payments: NewPaymentService(db, postgres.NewSubscriptionRepository(db), postgres.NewBkashTransactionRepository(db),
			postgres.NewCouponRepository(db), fb, "https://app.test", billing),
		orders: NewMarketplacePaymentService(db, orderRepo, postgres.NewOrderTransactionRepository(db), fb, "https://app.test", billing),
	}
}

func (e *env) user(t *testing.T) domain.User {
	t.Helper()
	u := domain.User{Email: uuid.NewString() + "@test.dev", FirstName: "Test", LastName: "User", TokenVersion: 1}
	if err := e.db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) plan(t *testing.T, price, days int, lifetime bool) domain.SubscriptionPlan {
	t.Helper()
	p := domain.SubscriptionPlan{Title: "Pro " + strconv.Itoa(days) + "d", Price: price, DurationDays: days, IsLifetime: lifetime}
	if err := e.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) coupon(t *testing.T, maxUses int, percent int) domain.CouponCode {
	t.Helper()
	c := domain.CouponCode{Code: "C" + uuid.NewString()[:8], DiscountType: domain.CouponPercentage, DiscountValue: percent, MaxUses: maxUses, IsActive: true}
	if err := e.db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *env) merchant(t *testing.T, commission float64, platform bool) domain.Merchant {
	t.Helper()
	owner := e.user(t)
	m := domain.Merchant{UserID: owner.ID, BusinessName: "Shop " + uuid.NewString()[:6], CommissionRate: commission,
		Status: domain.MerchantStatusApproved, IsPlatform: platform, PayoutMethod: "bkash", PayoutAccount: "01700000000"}
	if err := e.db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	return m
}

// product inserts a published product for merchant m.
func (e *env) product(t *testing.T, m domain.Merchant, price, stock int) domain.Product {
	t.Helper()
	p := domain.Product{MerchantID: m.ID, Title: "Item " + uuid.NewString()[:4], Price: price, Stock: stock, IsPublished: true}
	if err := e.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// deliveredOrderFor creates a delivered order for buyer containing p.
func (e *env) deliveredOrderFor(t *testing.T, buyer domain.User, m domain.Merchant, p domain.Product) domain.Order {
	t.Helper()
	o := domain.Order{BuyerID: buyer.ID, ShippingRecipientName: "R", ShippingPhone: "01", ShippingAddressLine: "A", ShippingCity: "C",
		Status: domain.OrderStatusDelivered, PaymentMethod: domain.PaymentMethodCashOnDelivery, TotalAmount: p.Price,
		Items: []domain.OrderItem{{ProductID: p.ID, ProductTitle: p.Title, MerchantID: m.ID, Quantity: 1, UnitPrice: p.Price}}}
	if err := e.db.Create(&o).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

// order creates an order for buyer with one line per (merchant, unitPrice, qty).
func (e *env) order(t *testing.T, buyer domain.User, method domain.PaymentMethod, status domain.OrderStatus, lines ...orderLine) domain.Order {
	t.Helper()
	o := domain.Order{BuyerID: buyer.ID, ShippingRecipientName: "R", ShippingPhone: "01", ShippingAddressLine: "A", ShippingCity: "C",
		Status: status, PaymentMethod: method}
	for _, l := range lines {
		o.TotalAmount += l.price * l.qty
		o.Items = append(o.Items, domain.OrderItem{ProductID: uuid.New(), ProductTitle: "Item", MerchantID: l.merchant.ID,
			Quantity: l.qty, UnitPrice: l.price, CommissionRateSnapshot: l.merchant.CommissionRate})
	}
	if err := e.db.Create(&o).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

type orderLine struct {
	merchant domain.Merchant
	price    int
	qty      int
}

func (e *env) deliver(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	if err := e.db.Model(&domain.Order{}).Where("id = ?", orderID).Update("status", domain.OrderStatusDelivered).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.billing.OnOrderDelivered(context.Background(), orderID); err != nil {
		t.Fatalf("OnOrderDelivered: %v", err)
	}
}

func (e *env) backdate(t *testing.T, table string, id uuid.UUID, age time.Duration) {
	t.Helper()
	if err := e.db.Exec("UPDATE "+table+" SET created_at = ? WHERE id = ?", time.Now().Add(-age), id).Error; err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *gorm.DB, model any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	q := db.Model(model)
	if where != "" {
		q = q.Where(where, args...)
	}
	if err := q.Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// assertLedgerBalanced fails if total debits != total credits anywhere.
func assertLedgerBalanced(t *testing.T, db *gorm.DB) {
	t.Helper()
	var sums struct{ D, C int }
	if err := db.Model(&domain.LedgerEntry{}).Select("COALESCE(SUM(debit),0) AS d, COALESCE(SUM(credit),0) AS c").Scan(&sums).Error; err != nil {
		t.Fatal(err)
	}
	if sums.D != sums.C {
		t.Fatalf("ledger unbalanced: debit=%d credit=%d", sums.D, sums.C)
	}
}

func reload[T any](t *testing.T, db *gorm.DB, id uuid.UUID) T {
	t.Helper()
	var v T
	if err := db.First(&v, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return v
}

// runConcurrently runs fn n times at once and returns the errors.
func runConcurrently(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}
