package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/bkash"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
)

var (
	ErrOrderNotFound          = errors.New("order not found")
	ErrOrderTxNotFound        = errors.New("order payment transaction not found")
	ErrMarketplacePaymentNotCompleted = errors.New("bkash marketplace payment was not completed")
)

// MarketplacePaymentService owns the bKash payment lifecycle for marketplace
// orders: starting a checkout session and, on execute, re-verifying with
// bKash directly before marking the Order as paid.
type MarketplacePaymentService struct {
	orderRepo       domain.OrderRepository
	txRepo          domain.OrderTransactionRepository
	bkashClient     *bkash.Client
	callbackBaseURL string
}

func NewMarketplacePaymentService(
	orderRepo domain.OrderRepository,
	txRepo domain.OrderTransactionRepository,
	bkashClient *bkash.Client,
	callbackBaseURL string,
) *MarketplacePaymentService {
	return &MarketplacePaymentService{
		orderRepo:       orderRepo,
		txRepo:          txRepo,
		bkashClient:     bkashClient,
		callbackBaseURL: callbackBaseURL,
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
// always read from the order's TotalAmount server-side.
func (s *MarketplacePaymentService) CreatePayment(ctx context.Context, userID, orderID uuid.UUID) (*MarketplaceCreatePaymentResult, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.BuyerID != userID {
		return nil, ErrForbidden
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

	if order.Status == domain.OrderStatusPaid || order.Status == domain.OrderStatusProcessing ||
		order.Status == domain.OrderStatusShipped || order.Status == domain.OrderStatusDelivered {
		return nil // already paid/fulfilled, idempotent
	}

	result, err := s.bkashClient.ExecutePayment(ctx, paymentID)
	if err != nil {
		return err
	}
	rawJSON, _ := json.Marshal(result)

	if result.TransactionStatus != "Completed" {
		if updateErr := s.txRepo.UpdateStatus(ctx, tx.ID, domain.BkashStatusFailed, result.TrxID, string(rawJSON)); updateErr != nil {
			logger.Errorf("[bkash] failed to record failed marketplace payment %s: %v", paymentID, updateErr)
		}
		return fmt.Errorf("%w: %s", ErrMarketplacePaymentNotCompleted, result.StatusMessage)
	}

	// Mark the order as paid before updating the transaction — same
	// fail-safe ordering reasoning as PaymentService.ExecutePayment.
	if err := s.orderRepo.UpdateStatus(ctx, tx.OrderID, domain.OrderStatusPaid); err != nil {
		return fmt.Errorf("failed to update order status to paid: %w", err)
	}
	if err := s.txRepo.UpdateStatus(ctx, tx.ID, domain.BkashStatusCompleted, result.TrxID, string(rawJSON)); err != nil {
		logger.Errorf("[bkash] order paid but failed to mark transaction %s completed: %v", paymentID, err)
	}

	return nil
}
