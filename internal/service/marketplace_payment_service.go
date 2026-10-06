package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/bkash"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOrderNotFound                  = errors.New("order not found")
	ErrOrderTxNotFound                = errors.New("order payment transaction not found")
	ErrMarketplacePaymentNotCompleted = errors.New("bkash marketplace payment was not completed")
	ErrOrderNotPayable                = errors.New("order is not awaiting bKash payment")
)

// MarketplacePaymentService owns the bKash payment lifecycle for marketplace
// orders: starting a checkout session and, on execute, re-verifying with
// bKash directly before marking the Order as paid. Like PaymentService,
// fulfilment is one locked transaction (order state + payment state + ledger
// + invoice) and a reconciliation pass recovers payments whose execute call
// never arrived.
type MarketplacePaymentService struct {
	db              *gorm.DB
	orderRepo       domain.OrderRepository
	txRepo          domain.OrderTransactionRepository
	bkashClient     BkashGateway
	callbackBaseURL string
	billing         *BillingService // optional; nil skips ledger + invoices
	notifier        *OrderNotifier  // optional; nil skips order notifications
}

// SetOrderNotifier enables buyer/seller notifications when an order is paid.
func (s *MarketplacePaymentService) SetOrderNotifier(n *OrderNotifier) { s.notifier = n }

func NewMarketplacePaymentService(
	db *gorm.DB,
	orderRepo domain.OrderRepository,
	txRepo domain.OrderTransactionRepository,
	bkashClient BkashGateway,
	callbackBaseURL string,
	billing *BillingService,
) *MarketplacePaymentService {
	return &MarketplacePaymentService{
		db:              db,
		orderRepo:       orderRepo,
		txRepo:          txRepo,
		bkashClient:     bkashClient,
		callbackBaseURL: callbackBaseURL,
		billing:         billing,
	}
}

type MarketplaceCreatePaymentResult struct {
	PaymentID  string `json:"payment_id"`
	BkashURL   string `json:"bkash_url"`
	SuccessURL string `json:"success_url"`
	FailureURL string `json:"failure_url"`
	CancelURL  string `json:"cancel_url"`
}

// CreatePayment starts a bKash checkout session for an Order. The amount is
// always read from the order's TotalAmount server-side. Only an unpaid bKash
// order can be paid — paying a cash-on-delivery or already-paid order would
// charge the buyer twice for nothing.
func (s *MarketplacePaymentService) CreatePayment(ctx context.Context, userID, orderID uuid.UUID) (*MarketplaceCreatePaymentResult, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.BuyerID != userID {
		return nil, ErrForbidden
	}
	if order.Status != domain.OrderStatusPendingPayment || order.PaymentMethod != domain.PaymentMethodBkash {
		return nil, ErrOrderNotPayable
	}

	invoiceNumber := uuid.New().String()
	payerReference := " "
	if !s.bkashClient.IsProduction() {
		payerReference = "01929918378" // bKash sandbox test MSISDN
	}

	callbackURL := fmt.Sprintf("%s/payment?order_id=%s&amount=%d",
		s.callbackBaseURL, orderID, order.TotalAmount)

	result, err := s.bkashClient.CreatePayment(ctx, order.TotalAmount, invoiceNumber, payerReference, callbackURL)
	if err != nil {
		return nil, err
	}

	tx := &domain.OrderTransaction{
		OrderID:   orderID,
		PaymentID: result.PaymentID,
		Amount:    order.TotalAmount,
		Status:    domain.BkashStatusInitiated,
	}
	if err := s.txRepo.Create(ctx, tx); err != nil {
		return nil, fmt.Errorf("failed to persist order payment transaction: %w", err)
	}

	return &MarketplaceCreatePaymentResult{
		PaymentID:  result.PaymentID,
		BkashURL:   result.BkashURL,
		SuccessURL: result.SuccessCallbackURL,
		FailureURL: result.FailureCallbackURL,
		CancelURL:  result.CancelledCallbackURL,
	}, nil
}

// ExecutePayment re-verifies with bKash directly. On success, marks the
// Order as paid. Idempotent: a second call for an already-paid order just
// returns nil.
func (s *MarketplacePaymentService) ExecutePayment(ctx context.Context, userID uuid.UUID, paymentID string) error {
	tx, err := s.txRepo.GetByPaymentID(ctx, paymentID)
	if err != nil {
		return ErrOrderTxNotFound
	}

	order, err := s.orderRepo.GetByID(ctx, tx.OrderID)
	if err != nil {
		return ErrOrderNotFound
	}
	if order.BuyerID != userID {
		return ErrForbidden
	}
	if tx.Status == domain.BkashStatusCompleted {
		return nil // already paid, idempotent
	}
	if tx.Status == domain.BkashStatusNeedsReview {
		return ErrAmountMismatch
	}

	result, err := s.bkashClient.ExecutePayment(ctx, paymentID)
	if err != nil || result.TransactionStatus != bkashCompleted {
		// See PaymentService.ExecutePayment: a failed execute is not proof
		// the buyer wasn't charged, so ask bKash for the real state.
		if queried, qErr := s.bkashClient.QueryPayment(ctx, paymentID); qErr == nil && queried.TransactionStatus == bkashCompleted {
			result, err = queried, nil
		}
	}
	if err != nil {
		return err
	}
	rawJSON, _ := json.Marshal(result)

	if result.TransactionStatus != bkashCompleted {
		s.markTerminal(ctx, paymentID, domain.BkashStatusFailed, result.TrxID, string(rawJSON))
		return fmt.Errorf("%w: %s", ErrMarketplacePaymentNotCompleted, result.StatusMessage)
	}
	return s.fulfil(ctx, paymentID, result.TrxID, string(rawJSON), result)
}

// fulfil marks the order paid, completes the payment record and books the
// ledger/invoice in one transaction, exactly once.
func (s *MarketplacePaymentService) fulfil(ctx context.Context, paymentID, trxID, rawJSON string, confirmed *bkash.ExecutePaymentResult) error {
	// outcome carries "flagged for review" results out of the transaction:
	// returning them from the closure would roll back the review flag itself.
	var outcome error
	var paidOrderID uuid.UUID
	err := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		var tx domain.OrderTransaction
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tx, "payment_id = ?", paymentID).Error; err != nil {
			return ErrOrderTxNotFound
		}
		if tx.Status == domain.BkashStatusCompleted {
			return nil
		}

		flag := func(reason string) error {
			logger.Errorf("[bkash] marketplace payment %s needs review: %s", paymentID, reason)
			return db.Model(&domain.OrderTransaction{}).Where("id = ?", tx.ID).Updates(map[string]interface{}{
				"status": domain.BkashStatusNeedsReview, "trx_id": trxID, "raw_response": rawJSON,
			}).Error
		}

		if amt, ok := paidAmount(confirmed); ok && amt != tx.Amount {
			outcome = ErrAmountMismatch
			return flag(fmt.Sprintf("bKash reports %d, we charged %d", amt, tx.Amount))
		}

		var order domain.Order
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", tx.OrderID).Error; err != nil {
			return ErrOrderNotFound
		}
		if order.Status != domain.OrderStatusPendingPayment {
			// Paid, but the order moved on (cancelled by an admin while the
			// buyer was at the bKash page). Don't resurrect it; a human
			// decides about the refund.
			outcome = ErrOrderNotPayable
			return flag(fmt.Sprintf("order is %s, not pending_payment", order.Status))
		}

		if err := db.Model(&domain.Order{}).Where("id = ?", order.ID).Update("status", domain.OrderStatusPaid).Error; err != nil {
			return fmt.Errorf("failed to update order status to paid: %w", err)
		}
		if err := recordOrderEvent(db, order.ID, domain.OrderStatusPaid, uuid.Nil); err != nil {
			return err
		}
		paidOrderID = order.ID
		if err := db.Model(&domain.OrderTransaction{}).Where("id = ?", tx.ID).Updates(map[string]interface{}{
			"status": domain.BkashStatusCompleted, "trx_id": trxID, "raw_response": rawJSON,
		}).Error; err != nil {
			return err
		}
		return s.billing.recordOrderPayment(db, &order, trxID)
	})
	if err != nil {
		return err
	}
	if outcome == nil && paidOrderID != uuid.Nil {
		s.notifier.OrderStatusChanged(ctx, paidOrderID, domain.OrderStatusPaid)
	}
	return outcome
}

// markTerminal closes an initiated payment as failed/cancelled. Anything not
// still "initiated" is left alone, so a completed payment can't be undone.
func (s *MarketplacePaymentService) markTerminal(ctx context.Context, paymentID string, status domain.BkashTransactionStatus, trxID, raw string) {
	err := s.db.WithContext(ctx).Model(&domain.OrderTransaction{}).
		Where("payment_id = ? AND status = ?", paymentID, domain.BkashStatusInitiated).
		Updates(map[string]interface{}{"status": status, "trx_id": trxID, "raw_response": raw}).Error
	if err != nil {
		logger.Errorf("[bkash] failed to record %s for marketplace payment %s: %v", status, paymentID, err)
	}
}

// ReconcilePending is PaymentService.ReconcilePending for marketplace
// orders: payments still "initiated" after minAge are checked with bKash;
// completed ones are fulfilled, the rest older than cancelAfter are closed.
func (s *MarketplacePaymentService) ReconcilePending(ctx context.Context, minAge, cancelAfter time.Duration, batch int) (ReconcileStats, error) {
	var stats ReconcileStats
	var pending []domain.OrderTransaction
	if err := s.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", domain.BkashStatusInitiated, time.Now().Add(-minAge)).
		Order("created_at asc").Limit(batch).Find(&pending).Error; err != nil {
		return stats, err
	}

	for _, p := range pending {
		stats.Checked++
		result, err := s.bkashClient.QueryPayment(ctx, p.PaymentID)
		if err != nil {
			stats.Errors++
			logger.Errorf("[reconcile] bkash query %s: %v", p.PaymentID, err)
			continue
		}
		if result.TransactionStatus == bkashCompleted {
			raw, _ := json.Marshal(result)
			if err := s.fulfil(ctx, p.PaymentID, result.TrxID, string(raw), result); err != nil {
				stats.Errors++
				logger.Errorf("[reconcile] fulfil order payment %s: %v", p.PaymentID, err)
				continue
			}
			stats.Recovered++
			logger.Infof("[reconcile] recovered order payment %s (order %s)", p.PaymentID, p.OrderID)
			continue
		}
		if p.CreatedAt.Before(time.Now().Add(-cancelAfter)) {
			s.markTerminal(ctx, p.PaymentID, domain.BkashStatusCancelled, "", "")
			stats.Cancelled++
		}
	}
	return stats, nil
}
