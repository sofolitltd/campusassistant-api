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
	"gorm.io/gorm/clause"
)

var (
	ErrPlanNotFound        = errors.New("subscription plan not found")
	ErrTransactionNotFound = errors.New("payment transaction not found")
	ErrForbidden           = errors.New("payment does not belong to this user")
	ErrPaymentNotCompleted = errors.New("bkash payment was not completed")
	ErrAmountMismatch      = errors.New("bkash reported a different amount than was charged; flagged for review")
	ErrCouponNotFound      = errors.New("coupon code not found")
	ErrCouponExpired       = errors.New("coupon code has expired")
	ErrCouponExhausted     = errors.New("coupon code has reached its usage limit")
	ErrCouponMinAmount     = errors.New("plan price does not meet the coupon minimum")
	ErrCouponPlanMismatch  = errors.New("coupon does not apply to this plan")
)

// PaymentService owns the bKash payment lifecycle: starting a checkout
// session and, on execute, re-verifying with bKash directly (never trusting
// a client-supplied "success" claim) before granting a subscription.
//
// Fulfilment is the one place money turns into entitlement, and it is built
// to be safe under every way a payment can arrive more than once — a double
// tap, a retry, the reconciliation job racing the user's own execute call:
// it runs in a single DB transaction, takes a row lock on the payment
// first, and does nothing if the payment is already completed.
type PaymentService struct {
	db              *gorm.DB
	subRepo         domain.SubscriptionRepository
	txRepo          domain.BkashTransactionRepository
	couponRepo      domain.CouponRepository
	bkashClient     BkashGateway
	callbackBaseURL string
	billing         *BillingService // optional; nil skips ledger + invoices
}

func NewPaymentService(db *gorm.DB, subRepo domain.SubscriptionRepository, txRepo domain.BkashTransactionRepository, couponRepo domain.CouponRepository, bkashClient BkashGateway, callbackBaseURL string, billing *BillingService) *PaymentService {
	return &PaymentService{
		db:              db,
		subRepo:         subRepo,
		txRepo:          txRepo,
		couponRepo:      couponRepo,
		bkashClient:     bkashClient,
		callbackBaseURL: callbackBaseURL,
		billing:         billing,
	}
}

type CreatePaymentResult struct {
	PaymentID        string `json:"payment_id"`
	BkashURL         string `json:"bkash_url"`
	SuccessURL       string `json:"success_url"`
	FailureURL       string `json:"failure_url"`
	CancelURL        string `json:"cancel_url"`
	DiscountedAmount *int   `json:"discounted_amount,omitempty"`
	OriginalAmount   *int   `json:"original_amount,omitempty"`
}

// ValidateCoupon checks whether a coupon code is valid for the given plan
// and returns the discounted amount. Returns an error if the coupon is
// expired, exhausted, plan-mismatched, or below minimum amount. This is a
// read-only preview; the use is actually claimed atomically in
// reserveCoupon when checkout starts.
func (s *PaymentService) ValidateCoupon(ctx context.Context, code string, planID uuid.UUID, planPrice int) (int, error) {
	coupon, err := s.couponRepo.GetByCode(ctx, code)
	if err != nil {
		return 0, ErrCouponNotFound
	}
	return validateCoupon(coupon, planID, planPrice)
}

func validateCoupon(coupon *domain.CouponCode, planID uuid.UUID, planPrice int) (int, error) {
	if !coupon.IsActive {
		return 0, ErrCouponNotFound
	}
	if coupon.ExpiresAt != nil && coupon.ExpiresAt.Before(time.Now()) {
		return 0, ErrCouponExpired
	}
	if coupon.MaxUses > 0 && coupon.UsedCount >= coupon.MaxUses {
		return 0, ErrCouponExhausted
	}
	if coupon.MinAmount > 0 && planPrice < coupon.MinAmount {
		return 0, ErrCouponMinAmount
	}
	if coupon.PlanID != nil && *coupon.PlanID != planID {
		return 0, ErrCouponPlanMismatch
	}

	switch coupon.DiscountType {
	case domain.CouponPercentage:
		discount := planPrice * coupon.DiscountValue / 100
		if discount > planPrice {
			return 0, nil
		}
		return planPrice - discount, nil
	case domain.CouponFixed:
		if coupon.DiscountValue >= planPrice {
			return 0, nil // coupon makes it free
		}
		return planPrice - coupon.DiscountValue, nil
	default:
		return planPrice, nil
	}
}

// reserveCoupon atomically claims one use of a coupon. The conditional
// UPDATE is the real enforcement of MaxUses: two checkouts racing for the
// last use cannot both succeed.
func reserveCoupon(db *gorm.DB, couponID uuid.UUID) error {
	res := db.Model(&domain.CouponCode{}).
		Where("id = ? AND is_active = ? AND (max_uses = 0 OR used_count < max_uses)", couponID, true).
		UpdateColumn("used_count", gorm.Expr("used_count + 1"))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCouponExhausted
	}
	return nil
}

// forceCouponUse counts a use without checking the cap — for a payment that
// already happened after its reservation had been released.
func forceCouponUse(db *gorm.DB, couponID uuid.UUID) error {
	return db.Model(&domain.CouponCode{}).Where("id = ?", couponID).
		UpdateColumn("used_count", gorm.Expr("used_count + 1")).Error
}

func releaseCoupon(db *gorm.DB, couponID uuid.UUID) error {
	return db.Model(&domain.CouponCode{}).Where("id = ? AND used_count > 0", couponID).
		UpdateColumn("used_count", gorm.Expr("used_count - 1")).Error
}

// CreatePayment starts a bKash checkout session for planID. The amount is
// always read from the plan's price server-side — a client can never
// influence what actually gets charged. An optional couponCode can be
// provided to apply a discount; its use is reserved up front and released if
// the payment fails, is cancelled or expires.
func (s *PaymentService) CreatePayment(ctx context.Context, userID, planID uuid.UUID, couponCode string) (*CreatePaymentResult, error) {
	var plan domain.SubscriptionPlan
	if err := s.db.WithContext(ctx).First(&plan, "id = ?", planID).Error; err != nil {
		return nil, ErrPlanNotFound
	}

	chargeAmount := plan.Price
	var couponID *uuid.UUID

	if couponCode != "" {
		coupon, err := s.couponRepo.GetByCode(ctx, couponCode)
		if err != nil {
			return nil, ErrCouponNotFound
		}
		discounted, err := validateCoupon(coupon, planID, plan.Price)
		if err != nil {
			return nil, err
		}
		if err := reserveCoupon(s.db.WithContext(ctx), coupon.ID); err != nil {
			return nil, err
		}
		chargeAmount = discounted
		couponID = &coupon.ID
	}
	// From here on, any failure must hand the reserved use back.
	releaseOnError := func() {
		if couponID != nil {
			if err := releaseCoupon(s.db, *couponID); err != nil {
				logger.Errorf("[bkash] failed to release coupon %s after checkout error: %v", couponID, err)
			}
		}
	}

	invoiceNumber := uuid.New().String()
	payerReference := " "
	if !s.bkashClient.IsProduction() {
		payerReference = "01929918378" // bKash sandbox test MSISDN
	}

	callbackURL := fmt.Sprintf("%s/payment?plan_id=%s&plan_title=%s&amount=%d",
		s.callbackBaseURL, planID, url.QueryEscape(plan.Title), plan.Price)

	result, err := s.bkashClient.CreatePayment(ctx, chargeAmount, invoiceNumber, payerReference, callbackURL)
	if err != nil {
		releaseOnError()
		return nil, err
	}

	tx := &domain.BkashTransaction{
		UserID:         userID,
		PlanID:         planID,
		PaymentID:      result.PaymentID,
		Amount:         chargeAmount,
		OriginalAmount: plan.Price,
		Status:         domain.BkashStatusInitiated,
		CouponCodeID:   couponID,
		CouponReserved: couponID != nil,
	}
	if err := s.txRepo.Create(ctx, tx); err != nil {
		releaseOnError()
		return nil, fmt.Errorf("failed to persist payment transaction: %w", err)
	}

	res := &CreatePaymentResult{
		PaymentID:  result.PaymentID,
		BkashURL:   result.BkashURL,
		SuccessURL: result.SuccessCallbackURL,
		FailureURL: result.FailureCallbackURL,
		CancelURL:  result.CancelledCallbackURL,
	}
	if couponID != nil {
		discounted := chargeAmount
		res.DiscountedAmount = &discounted
		orig := plan.Price
		res.OriginalAmount = &orig
	}

	return res, nil
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
		return s.subscriptionFor(ctx, tx)
	}
	if tx.Status == domain.BkashStatusNeedsReview {
		return nil, ErrAmountMismatch
	}

	result, err := s.bkashClient.ExecutePayment(ctx, paymentID)
	if err != nil || result.TransactionStatus != bkashCompleted {
		// Execute failing does not prove the user wasn't charged: the
		// response may have been lost, or a concurrent execute (another
		// instance, the reconciler) may have already completed it — bKash
		// rejects the second execute. Ask bKash for the real state.
		if queried, qErr := s.bkashClient.QueryPayment(ctx, paymentID); qErr == nil && queried.TransactionStatus == bkashCompleted {
			result, err = queried, nil
		}
	}
	if err != nil {
		return nil, err
	}
	rawJSON, _ := json.Marshal(result)

	if result.TransactionStatus != bkashCompleted {
		s.markTerminal(ctx, paymentID, domain.BkashStatusFailed, result.TrxID, string(rawJSON))
		return nil, fmt.Errorf("%w: %s", ErrPaymentNotCompleted, result.StatusMessage)
	}

	return s.fulfil(ctx, paymentID, result.TrxID, string(rawJSON), result)
}

// fulfil turns a bKash-confirmed payment into a subscription, an invoice
// and ledger entries — atomically, exactly once.
func (s *PaymentService) fulfil(ctx context.Context, paymentID, trxID, rawJSON string, confirmed *bkash.ExecutePaymentResult) (*domain.UserSubscription, error) {
	var sub *domain.UserSubscription
	var mismatch bool

	err := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		// Row lock first: whoever gets here second waits, then sees
		// "completed" below and returns without granting again.
		var tx domain.BkashTransaction
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tx, "payment_id = ?", paymentID).Error; err != nil {
			return ErrTransactionNotFound
		}
		if tx.Status == domain.BkashStatusCompleted {
			existing, err := s.subscriptionForDB(db, &tx)
			sub = existing
			return err
		}

		if amt, ok := paidAmount(confirmed); ok && amt != tx.Amount {
			mismatch = true
			logger.Errorf("[bkash] payment %s: bKash reports %d, we charged %d — flagged for review", paymentID, amt, tx.Amount)
			return db.Model(&domain.BkashTransaction{}).Where("id = ?", tx.ID).Updates(map[string]interface{}{
				"status": domain.BkashStatusNeedsReview, "trx_id": trxID, "raw_response": rawJSON,
			}).Error
		}

		var plan domain.SubscriptionPlan
		// Unscoped: a plan deleted after checkout started must still be honoured.
		if err := db.Unscoped().First(&plan, "id = ?", tx.PlanID).Error; err != nil {
			return ErrPlanNotFound
		}

		grantReason := "payment"
		if tx.CouponCodeID != nil {
			grantReason = "coupon"
		}
		granted, err := grantSubscription(db, tx.UserID, &plan, float64(tx.Amount), grantReason, nil)
		if err != nil {
			return err
		}

		// The coupon use was reserved at checkout. If the reservation was
		// released meanwhile (the payment was marked cancelled/failed but
		// bKash later confirmed it), take it again — the user did pay.
		if tx.CouponCodeID != nil && !tx.CouponReserved {
			if err := forceCouponUse(db, *tx.CouponCodeID); err != nil {
				return err
			}
		}

		if err := db.Model(&domain.BkashTransaction{}).Where("id = ?", tx.ID).Updates(map[string]interface{}{
			"status":          domain.BkashStatusCompleted,
			"trx_id":          trxID,
			"raw_response":    rawJSON,
			"subscription_id": granted.ID,
			"coupon_reserved": tx.CouponCodeID != nil,
		}).Error; err != nil {
			return err
		}
		tx.TrxID, tx.Status = trxID, domain.BkashStatusCompleted
		if err := s.billing.recordSubscriptionPayment(db, &tx, plan.Title); err != nil {
			return err
		}
		sub = granted
		return nil
	})
	if err != nil {
		return nil, err
	}
	if mismatch {
		return nil, ErrAmountMismatch
	}
	return sub, nil
}

func (s *PaymentService) subscriptionFor(ctx context.Context, tx *domain.BkashTransaction) (*domain.UserSubscription, error) {
	return s.subscriptionForDB(s.db.WithContext(ctx), tx)
}

func (s *PaymentService) subscriptionForDB(db *gorm.DB, tx *domain.BkashTransaction) (*domain.UserSubscription, error) {
	var sub domain.UserSubscription
	q := db.Preload("User")
	if tx.SubscriptionID != nil {
		q = q.Where("id = ?", *tx.SubscriptionID)
	} else {
		// Completed before subscription_id was recorded: newest for the user.
		q = q.Where("user_id = ?", tx.UserID).Order("created_at desc")
	}
	if err := q.First(&sub).Error; err != nil {
		return nil, err
	}
	return &sub, nil
}

// grantSubscription records a subscription and updates the user's Pro state.
// Renewals stack: buying while already Pro extends from the current expiry
// instead of overwriting it, and a lifetime user never gets downgraded to a
// dated expiry. The user row is locked so concurrent grants serialise.
func grantSubscription(db *gorm.DB, userID uuid.UUID, plan *domain.SubscriptionPlan, price float64, reason string, grantedBy *uuid.UUID) (*domain.UserSubscription, error) {
	var user domain.User
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}

	now := time.Now()
	endDate := subscriptionEnd(now, user.IsPro, user.ProExpiry, plan)

	sub := &domain.UserSubscription{
		UserID:      userID,
		PlanID:      plan.ID,
		Plan:        plan.Title,
		Price:       price,
		StartDate:   now,
		EndDate:     endDate,
		GrantedBy:   grantedBy,
		GrantReason: reason,
	}
	if err := db.Create(sub).Error; err != nil {
		return nil, fmt.Errorf("failed to create subscription: %w", err)
	}
	if err := db.Model(&domain.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"is_pro":     true,
		"pro_expiry": endDate,
	}).Error; err != nil {
		return nil, fmt.Errorf("failed to update user pro status: %w", err)
	}
	return sub, nil
}

// subscriptionEnd computes when access ends. nil means "never" (lifetime).
func subscriptionEnd(now time.Time, isPro bool, currentExpiry *time.Time, plan *domain.SubscriptionPlan) *time.Time {
	if plan.IsLifetime {
		return nil
	}
	if isPro && currentExpiry == nil {
		return nil // already lifetime — a dated plan must not shorten it
	}
	base := now
	if isPro && currentExpiry != nil && currentExpiry.After(now) {
		base = *currentExpiry
	}
	end := base.AddDate(0, 0, plan.DurationDays)
	return &end
}

// markTerminal moves an initiated payment to failed/cancelled and gives its
// coupon use back. A payment in any other state is left untouched, so this
// can never undo a completed payment.
func (s *PaymentService) markTerminal(ctx context.Context, paymentID string, status domain.BkashTransactionStatus, trxID, raw string) {
	err := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		var tx domain.BkashTransaction
		if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tx, "payment_id = ?", paymentID).Error; err != nil {
			return err
		}
		if tx.Status != domain.BkashStatusInitiated {
			return nil
		}
		if tx.CouponCodeID != nil && tx.CouponReserved {
			if err := releaseCoupon(db, *tx.CouponCodeID); err != nil {
				return err
			}
		}
		return db.Model(&domain.BkashTransaction{}).Where("id = ?", tx.ID).Updates(map[string]interface{}{
			"status": status, "trx_id": trxID, "raw_response": raw, "coupon_reserved": false,
		}).Error
	})
	if err != nil {
		logger.Errorf("[bkash] failed to record %s for payment %s: %v", status, paymentID, err)
	}
}

// CancelPayment marks an initiated payment transaction as cancelled. This is
// called when the user navigates away or bKash reports the user cancelled —
// it does NOT call bKash's own API (the session simply expires on its own).
// The reconciler catches initiated transactions that the client never had a
// chance to report. A payment later found to be completed is still fulfilled.
func (s *PaymentService) CancelPayment(ctx context.Context, userID uuid.UUID, paymentID string) error {
	tx, err := s.txRepo.GetByPaymentID(ctx, paymentID)
	if err != nil {
		return ErrTransactionNotFound
	}
	if tx.UserID != userID {
		return ErrForbidden
	}
	if tx.Status != domain.BkashStatusInitiated {
		return nil
	}
	s.markTerminal(ctx, paymentID, domain.BkashStatusCancelled, "", "")
	return nil
}

// ReconcileStats reports what a reconciliation pass did.
type ReconcileStats struct {
	Checked   int
	Recovered int // paid at bKash but never executed by the client — now fulfilled
	Cancelled int // abandoned checkouts closed out
	Errors    int
}

// ReconcilePending closes the gap that client-driven execute leaves open: a
// user can pay at bKash and then lose the app/connection before calling
// execute, so we never learn the payment succeeded. For every payment still
// "initiated" after minAge it asks bKash for the truth:
//
//   - Completed → fulfil it now (subscription, invoice, ledger), as if the
//     client had executed it;
//   - anything else, and older than cancelAfter → cancel it and release its
//     coupon use;
//   - bKash unreachable → leave it for the next pass.
//
// Safe to run on every instance at once: fulfil() locks the payment row.
func (s *PaymentService) ReconcilePending(ctx context.Context, minAge, cancelAfter time.Duration, batch int) (ReconcileStats, error) {
	var stats ReconcileStats
	var pending []domain.BkashTransaction
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
			if _, err := s.fulfil(ctx, p.PaymentID, result.TrxID, string(raw), result); err != nil {
				stats.Errors++
				logger.Errorf("[reconcile] fulfil %s: %v", p.PaymentID, err)
				continue
			}
			stats.Recovered++
			logger.Infof("[reconcile] recovered subscription payment %s (user %s)", p.PaymentID, p.UserID)
			continue
		}
		if p.CreatedAt.Before(time.Now().Add(-cancelAfter)) {
			s.markTerminal(ctx, p.PaymentID, domain.BkashStatusCancelled, "", "")
			stats.Cancelled++
		}
	}
	return stats, nil
}

// ListTransactions returns the authenticated user's own bKash payment
// history, newest first — used by the profile page's transaction details.
func (s *PaymentService) ListTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.BkashTransaction, int64, error) {
	return s.txRepo.ListByUser(ctx, userID, limit, offset)
}

// AdminGrantPro grants a subscription to a user without payment. The price is
// set to 0 and the grant_reason is "admin_grant". Like a purchase it stacks
// on an existing subscription, and the hourly sweeper expires it on time.
func (s *PaymentService) AdminGrantPro(ctx context.Context, adminID, userID, planID uuid.UUID, note string) (*domain.UserSubscription, error) {
	var plan domain.SubscriptionPlan
	if err := s.db.WithContext(ctx).First(&plan, "id = ?", planID).Error; err != nil {
		return nil, ErrPlanNotFound
	}
	var sub *domain.UserSubscription
	err := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		granted, err := grantSubscription(db, userID, &plan, 0, "admin_grant", &adminID)
		sub = granted
		return err
	})
	if err != nil {
		return nil, err
	}
	logger.Infof("[audit] admin %s granted plan %s to user %s (note: %q)", adminID, planID, userID, note)
	return sub, nil
}
