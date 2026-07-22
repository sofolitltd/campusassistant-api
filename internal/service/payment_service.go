package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/bkash"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrPlanNotFound        = errors.New("subscription plan not found")
	ErrTransactionNotFound = errors.New("payment transaction not found")
	ErrForbidden           = errors.New("payment does not belong to this user")
	ErrPaymentNotCompleted = errors.New("bkash payment was not completed")
)

// PaymentService owns the bKash payment lifecycle: starting a checkout
// session and, on execute, re-verifying with bKash directly (never trusting
// a client-supplied "success" claim) before granting a subscription.
type PaymentService struct {
	db              *gorm.DB
	subRepo         domain.SubscriptionRepository
	txRepo          domain.BkashTransactionRepository
	bkashClient     *bkash.Client
	callbackBaseURL string
}

func NewPaymentService(db *gorm.DB, subRepo domain.SubscriptionRepository, txRepo domain.BkashTransactionRepository, bkashClient *bkash.Client, callbackBaseURL string) *PaymentService {
	return &PaymentService{
		db:              db,
		subRepo:         subRepo,
		txRepo:          txRepo,
		bkashClient:     bkashClient,
		callbackBaseURL: callbackBaseURL,
	}
}

type CreatePaymentResult struct {
	PaymentID  string `json:"payment_id"`
	BkashURL   string `json:"bkash_url"`
	SuccessURL string `json:"success_url"`
	FailureURL string `json:"failure_url"`
	CancelURL  string `json:"cancel_url"`
}

// CreatePayment starts a bKash checkout session for planID. The amount is
// always read from the plan's price server-side — a client can never
// influence what actually gets charged.
func (s *PaymentService) CreatePayment(ctx context.Context, userID, planID uuid.UUID) (*CreatePaymentResult, error) {
	var plan domain.SubscriptionPlan
	if err := s.db.WithContext(ctx).First(&plan, "id = ?", planID).Error; err != nil {
		return nil, ErrPlanNotFound
	}

	invoiceNumber := uuid.New().String()
	payerReference := " "
	if !s.bkashClient.IsProduction() {
		payerReference = "01929918378" // bKash sandbox test MSISDN
	}

	// bKash appends its own ?paymentID=...&status=... to this single URL and
	// echoes back outcome-specific variants in the create response — we
	// don't need three separate URLs here, only a place for the browser/
	// WebView to land after payment. plan_title/amount are display-only —
	// the actual charge always comes from plan.Price above, never from
	// anything echoed back through this URL.
	callbackURL := fmt.Sprintf("%s/payment?plan_id=%s&plan_title=%s&amount=%d",
		s.callbackBaseURL, planID, url.QueryEscape(plan.Title), plan.Price)

	result, err := s.bkashClient.CreatePayment(ctx, plan.Price, invoiceNumber, payerReference, callbackURL)
	if err != nil {
		return nil, err
	}

	tx := &domain.BkashTransaction{
		UserID:    userID,
		PlanID:    planID,
		PaymentID: result.PaymentID,
		Amount:    plan.Price,
		Status:    domain.BkashStatusInitiated,
	}
	if err := s.txRepo.Create(ctx, tx); err != nil {
		return nil, fmt.Errorf("failed to persist payment transaction: %w", err)
	}

	return &CreatePaymentResult{
		PaymentID:  result.PaymentID,
		BkashURL:   result.BkashURL,
		SuccessURL: result.SuccessCallbackURL,
		FailureURL: result.FailureCallbackURL,
		CancelURL:  result.CancelledCallbackURL,
	}, nil
}

// ExecutePayment is the single source of truth for whether a payment
// completed. It re-verifies with bKash directly — a forged "success" URL
// from the client only ever triggers a real re-verification, which bKash
// will reject if the payment was never actually completed. Idempotent: a
// second call for an already-completed payment returns the existing
// subscription without hitting bKash again.
func (s *PaymentService) ExecutePayment(ctx context.Context, userID uuid.UUID, paymentID string) (*domain.UserSubscription, error) {
	tx, err := s.txRepo.GetByPaymentID(ctx, paymentID)
	if err != nil {
		return nil, ErrTransactionNotFound
	}
	if tx.UserID != userID {
		return nil, ErrForbidden
	}

	if tx.Status == domain.BkashStatusCompleted {
		return s.subRepo.GetUserSubscription(ctx, userID)
	}

	result, err := s.bkashClient.ExecutePayment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	rawJSON, _ := json.Marshal(result)

	if result.TransactionStatus != "Completed" {
		if updateErr := s.txRepo.UpdateStatus(ctx, tx.ID, domain.BkashStatusFailed, result.TrxID, string(rawJSON)); updateErr != nil {
			logger.Errorf("[bkash] failed to record failed transaction %s: %v", paymentID, updateErr)
		}
		return nil, fmt.Errorf("%w: %s", ErrPaymentNotCompleted, result.StatusMessage)
	}

	var plan domain.SubscriptionPlan
	if err := s.db.WithContext(ctx).First(&plan, "id = ?", tx.PlanID).Error; err != nil {
		return nil, ErrPlanNotFound
	}

	now := time.Now()
	var endDate *time.Time
	if !plan.IsLifetime {
		computed := now.AddDate(0, 0, plan.DurationDays)
		endDate = &computed
	}
	sub := &domain.UserSubscription{
		UserID:    userID,
		PlanID:    plan.ID,
		Plan:      plan.Title,
		StartDate: now,
		EndDate:   endDate,
	}

	// Grant the subscription before marking the transaction completed — if
	// something crashes between the two, the worst case is a subscription
	// with no matching "completed" transaction row (harmless: the user got
	// what they paid for), not the reverse (transaction says completed but
	// nothing was granted).
	if err := s.subRepo.CreateUserSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("failed to create subscription: %w", err)
	}
	if err := s.txRepo.UpdateStatus(ctx, tx.ID, domain.BkashStatusCompleted, result.TrxID, string(rawJSON)); err != nil {
		logger.Errorf("[bkash] subscription granted but failed to mark transaction %s completed: %v", paymentID, err)
	}

	return sub, nil
}

// ListTransactions returns the authenticated user's own bKash payment
// history, newest first — used by the profile page's transaction details.
func (s *PaymentService) ListTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.BkashTransaction, int64, error) {
	return s.txRepo.ListByUser(ctx, userID, limit, offset)
}
