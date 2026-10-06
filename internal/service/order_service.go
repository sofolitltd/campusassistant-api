package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidTransition   = errors.New("invalid order status change")
	ErrOrderNotCancellable = errors.New("this order can no longer be cancelled")
)

// StockError is returned by PlaceOrder when a line can't be fulfilled.
type StockError struct {
	Title     string
	Available int  // units left; 0 when sold out
	Gone      bool // product unpublished or deleted
}

func (e *StockError) Error() string {
	switch {
	case e.Gone:
		return e.Title + " is no longer available"
	case e.Available <= 0:
		return e.Title + " is out of stock"
	default:
		return fmt.Sprintf("Only %d of %s left", e.Available, e.Title)
	}
}

var orderRank = map[domain.OrderStatus]int{
	domain.OrderStatusPendingPayment: 0,
	domain.OrderStatusPaid:           1,
	domain.OrderStatusProcessing:     2,
	domain.OrderStatusShipped:        3,
	domain.OrderStatusDelivered:      4,
}

// CanTransition reports whether an order may move from one status to another:
// forward only (steps may be skipped, e.g. cash-on-delivery starts at
// processing), cancellation from any non-final status, and nothing out of
// delivered or cancelled.
func CanTransition(from, to domain.OrderStatus) bool {
	if from == to || from == domain.OrderStatusDelivered || from == domain.OrderStatusCancelled {
		return false
	}
	if to == domain.OrderStatusCancelled {
		return true
	}
	rf, okf := orderRank[from]
	rt, okt := orderRank[to]
	return okf && okt && rt > rf
}

// recordOrderEvent appends a timeline entry. Call it inside the same
// transaction that changes the order's status.
func recordOrderEvent(db *gorm.DB, orderID uuid.UUID, status domain.OrderStatus, actor uuid.UUID) error {
	ev := &domain.OrderEvent{OrderID: orderID, Status: status, ActorID: actor}
	return db.Create(ev).Error
}

// OrderService is the one place that changes an order's status after
// checkout: it validates the move, records the timeline entry, then — once
// committed — books earnings on delivery and notifies the people involved.
type OrderService struct {
	db       *gorm.DB
	billing  *BillingService
	notifier *OrderNotifier
	insights *InsightsService // optional; keeps seller shipping-speed fresh
}

// SetInsights enables the seller shipping-speed reputation update.
func (s *OrderService) SetInsights(i *InsightsService) { s.insights = i }

func NewOrderService(db *gorm.DB, billing *BillingService, notifier *OrderNotifier) *OrderService {
	return &OrderService{db: db, billing: billing, notifier: notifier}
}

// PlaceOrder creates an order and reserves its stock in one transaction: each
// line takes its quantity off the product with a conditional UPDATE, so two
// buyers can never both get the last unit. Any shortfall rolls everything
// back and returns a *StockError. Stock goes back when the order is cancelled.
func (s *OrderService) PlaceOrder(ctx context.Context, order *domain.Order) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, it := range order.Items {
			res := tx.Exec("UPDATE products SET stock = stock - ? WHERE id = ? AND is_published = ? AND stock >= ?",
				it.Quantity, it.ProductID, true, it.Quantity)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				var p domain.Product
				if err := tx.Select("title, stock, is_published").First(&p, "id = ?", it.ProductID).Error; err != nil {
					return &StockError{Title: it.ProductTitle, Gone: true}
				}
				return &StockError{Title: it.ProductTitle, Available: p.Stock, Gone: !p.IsPublished}
			}
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		return recordOrderEvent(tx, order.ID, order.Status, order.BuyerID)
	})
	if err != nil {
		return err
	}
	if order.Status == domain.OrderStatusProcessing {
		s.notifier.merchantsNewOrder(ctx, order.ID)
	}
	return nil
}

// restockOrder puts an order's units back on sale. Call it in the same
// transaction that cancels the order.
func restockOrder(tx *gorm.DB, orderID uuid.UUID) error {
	var items []domain.OrderItem
	if err := tx.Where("order_id = ?", orderID).Find(&items).Error; err != nil {
		return err
	}
	for _, it := range items {
		if err := tx.Exec("UPDATE products SET stock = stock + ? WHERE id = ?", it.Quantity, it.ProductID).Error; err != nil {
			return err
		}
	}
	return nil
}

// ExpireUnpaid cancels bKash orders nobody paid for, returning their stock to
// the shelf. An order is abandoned when it is older than maxAge and has no
// payment that is completed, awaiting review, or still in flight.
func (s *OrderService) ExpireUnpaid(ctx context.Context, maxAge time.Duration, batch int) (int, error) {
	cutoff := time.Now().Add(-maxAge)
	inFlight := time.Now().Add(-time.Hour)
	var ids []uuid.UUID
	err := s.db.WithContext(ctx).Model(&domain.Order{}).
		Where("status = ? AND payment_method = ? AND created_at < ?", domain.OrderStatusPendingPayment, domain.PaymentMethodBkash, cutoff).
		Where(`NOT EXISTS (SELECT 1 FROM order_transactions ot WHERE ot.order_id = orders.id AND
			(ot.status IN ? OR (ot.status = ? AND ot.created_at > ?)))`,
			[]domain.BkashTransactionStatus{domain.BkashStatusCompleted, domain.BkashStatusNeedsReview},
			domain.BkashStatusInitiated, inFlight).
		Order("created_at asc").Limit(batch).Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if err := s.Transition(ctx, id, domain.OrderStatusCancelled, uuid.Nil); err != nil {
			logger.Errorf("[orders] expire unpaid order %s: %v", id, err)
			continue
		}
		n++
	}
	return n, nil
}

// OnCreated records the order's first timeline entry. A cash-on-delivery
// order is already live (processing), so its sellers hear about it now; a
// bKash order waits for payment.
func (s *OrderService) OnCreated(ctx context.Context, order *domain.Order) error {
	if err := recordOrderEvent(s.db.WithContext(ctx), order.ID, order.Status, order.BuyerID); err != nil {
		return err
	}
	if order.Status == domain.OrderStatusProcessing {
		s.notifier.merchantsNewOrder(ctx, order.ID)
	}
	return nil
}

// Transition moves an order to a new status. Asking for the status the order
// already has is a no-op, so retries are safe.
func (s *OrderService) Transition(ctx context.Context, orderID uuid.UUID, to domain.OrderStatus, actor uuid.UUID) error {
	changed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", orderID).Error; err != nil {
			return ErrOrderNotFound
		}
		if order.Status == to {
			return nil
		}
		if !CanTransition(order.Status, to) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, order.Status, to)
		}
		if err := tx.Model(&domain.Order{}).Where("id = ?", order.ID).Update("status", to).Error; err != nil {
			return err
		}
		changed = true
		if to == domain.OrderStatusCancelled {
			if err := restockOrder(tx, order.ID); err != nil {
				return err
			}
		}
		return recordOrderEvent(tx, order.ID, to, actor)
	})
	if err != nil || !changed {
		return err
	}
	s.afterChange(ctx, orderID, to)
	return nil
}

// CancelByBuyer lets a buyer cancel their own order until it is on its way:
// unpaid orders, and cash-on-delivery orders that haven't shipped. A paid
// bKash order is never cancelled here — money has moved, so it goes through
// the admin refund flow.
func (s *OrderService) CancelByBuyer(ctx context.Context, userID, orderID uuid.UUID) error {
	changed, sellersKnew := false, false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", orderID).Error; err != nil {
			return ErrOrderNotFound
		}
		if order.BuyerID != userID {
			return ErrForbidden
		}
		if order.Status == domain.OrderStatusCancelled {
			return nil
		}
		cod := order.PaymentMethod == domain.PaymentMethodCashOnDelivery && order.Status == domain.OrderStatusProcessing
		if order.Status != domain.OrderStatusPendingPayment && !cod {
			return ErrOrderNotCancellable
		}
		if err := tx.Model(&domain.Order{}).Where("id = ?", order.ID).Update("status", domain.OrderStatusCancelled).Error; err != nil {
			return err
		}
		changed, sellersKnew = true, cod // sellers only hear about live orders
		if err := restockOrder(tx, order.ID); err != nil {
			return err
		}
		return recordOrderEvent(tx, order.ID, domain.OrderStatusCancelled, userID)
	})
	if err != nil || !changed {
		return err
	}
	if sellersKnew {
		s.notifier.merchantsCancelled(ctx, orderID)
	}
	return nil
}

// afterChange runs the side effects of a committed status change. Failures
// are logged, never returned: the change itself is already durable, and the
// billing sweeper retries earnings on its own.
func (s *OrderService) afterChange(ctx context.Context, orderID uuid.UUID, to domain.OrderStatus) {
	if to == domain.OrderStatusDelivered && s.billing != nil {
		if err := s.billing.OnOrderDelivered(ctx, orderID); err != nil {
			logger.Errorf("[billing] order %s delivered but earnings not booked yet (sweeper will retry): %v", orderID, err)
		}
	}
	if to == domain.OrderStatusShipped && s.insights != nil {
		s.refreshShipSpeed(ctx, orderID)
	}
	s.notifier.OrderStatusChanged(ctx, orderID, to)
}

// refreshShipSpeed recomputes the shipping-speed reputation of every seller
// on the order. Best-effort, like the other side effects.
func (s *OrderService) refreshShipSpeed(ctx context.Context, orderID uuid.UUID) {
	var merchantIDs []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&domain.OrderItem{}).Where("order_id = ?", orderID).Distinct().Pluck("merchant_id", &merchantIDs).Error; err != nil {
		return
	}
	for _, id := range merchantIDs {
		if id == uuid.Nil {
			continue
		}
		if err := s.insights.RecordShipSpeed(ctx, id); err != nil {
			logger.Errorf("[orders] ship speed for merchant %s: %v", id, err)
		}
	}
}

// ─── notifications ───────────────────────────────────────────────────────

// NotificationSender is the part of NotificationService the order flow needs.
type NotificationSender interface {
	SendToUsers(ctx context.Context, n domain.Notification, userIDs []uuid.UUID, createdBy uuid.UUID, notifyFns ...onCommitted) (*domain.Notification, []domain.NotificationRecipient, error)
}

// OrderNotifier tells buyers about their order's progress and sellers about
// new orders. Every method is nil-safe and best-effort, and respects the
// "marketplace" mute category.
type OrderNotifier struct {
	db     *gorm.DB
	sender NotificationSender
}

func NewOrderNotifier(db *gorm.DB, sender NotificationSender) *OrderNotifier {
	return &OrderNotifier{db: db, sender: sender}
}

const orderNotifType = "ORDER_UPDATE"

var buyerOrderMessages = map[domain.OrderStatus][2]string{
	domain.OrderStatusPaid:       {"Payment received", "We got your payment. The seller is preparing your order."},
	domain.OrderStatusProcessing: {"Order is being prepared", "The seller is getting your order ready."},
	domain.OrderStatusShipped:    {"Your order is on the way", "The seller has shipped your order."},
	domain.OrderStatusDelivered:  {"Order delivered", "Your order was delivered. Enjoying it? Leave a review."},
	domain.OrderStatusCancelled:  {"Order cancelled", "Your order has been cancelled."},
}

// OrderStatusChanged notifies the buyer of the new status; when an order has
// just been paid it also alerts the sellers.
func (n *OrderNotifier) OrderStatusChanged(ctx context.Context, orderID uuid.UUID, status domain.OrderStatus) {
	if n == nil || n.sender == nil {
		return
	}
	if msg, ok := buyerOrderMessages[status]; ok {
		var order domain.Order
		if err := n.db.WithContext(ctx).First(&order, "id = ?", orderID).Error; err == nil {
			n.send(ctx, order.BuyerID, msg[0], msg[1], orderID, status, "/campusmarket/orders/"+orderID.String())
		}
	}
	if status == domain.OrderStatusPaid {
		n.merchantsNewOrder(ctx, orderID)
	}
}

func (n *OrderNotifier) merchantsNewOrder(ctx context.Context, orderID uuid.UUID) {
	n.toMerchants(ctx, orderID, "New order received", "item", " — open your orders to start fulfilling it.")
}

func (n *OrderNotifier) merchantsCancelled(ctx context.Context, orderID uuid.UUID) {
	n.toMerchants(ctx, orderID, "Order cancelled by buyer", "item", " — no action needed.")
}

func (n *OrderNotifier) toMerchants(ctx context.Context, orderID uuid.UUID, title, noun, tail string) {
	if n == nil || n.sender == nil {
		return
	}
	var items []domain.OrderItem
	if err := n.db.WithContext(ctx).Where("order_id = ?", orderID).Find(&items).Error; err != nil {
		return
	}
	qty := map[uuid.UUID]int{}
	for _, it := range items {
		if it.MerchantID != uuid.Nil {
			qty[it.MerchantID] += it.Quantity
		}
	}
	for merchantID, q := range qty {
		var m domain.Merchant
		if err := n.db.WithContext(ctx).First(&m, "id = ?", merchantID).Error; err != nil {
			continue
		}
		label := fmt.Sprintf("%d %s", q, noun)
		if q != 1 {
			label += "s"
		}
		n.send(ctx, m.UserID, title, label+tail, orderID, "", "/merchant/manage/"+merchantID.String())
	}
}

func (n *OrderNotifier) send(ctx context.Context, userID uuid.UUID, title, body string, orderID uuid.UUID, status domain.OrderStatus, route string) {
	recipients, err := FilterMutedRecipients(ctx, n.db, []uuid.UUID{userID}, "marketplace", orderNotifType)
	if err != nil || len(recipients) == 0 {
		return
	}
	raw, err := json.Marshal(map[string]interface{}{"action_route": route, "order_id": orderID, "status": status})
	if err != nil {
		return
	}
	data := datatypes.JSON(raw)
	note := domain.Notification{Title: title, Body: body, Type: orderNotifType, Scope: "user", Data: &data}
	if _, _, err := n.sender.SendToUsers(ctx, note, recipients, uuid.Nil); err != nil {
		logger.Errorf("[orders] notify %s about order %s: %v", userID, orderID, err)
	}
}
