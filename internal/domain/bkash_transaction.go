package domain

import (
	"context"

	"github.com/google/uuid"
)

type BkashTransactionStatus string

const (
	BkashStatusInitiated BkashTransactionStatus = "initiated"
	BkashStatusCompleted BkashTransactionStatus = "completed"
	BkashStatusFailed    BkashTransactionStatus = "failed"
	BkashStatusCancelled BkashTransactionStatus = "cancelled"
	// BkashStatusNeedsReview marks a payment bKash reports as completed but
	// that we refused to fulfil automatically (amount mismatch, order since
	// cancelled, ...). Money moved, so an admin must decide — the
	// reconciliation job never touches these rows.
	BkashStatusNeedsReview BkashTransactionStatus = "needs_review"
	// BkashStatusRefunded: the money was returned to the payer (recorded by
	// an admin, see BillingService.RefundSubscriptionPayment/RefundOrder).
	BkashStatusRefunded BkashTransactionStatus = "refunded"
)

// BkashTransaction is the server-side audit trail + idempotency key for a
// bKash payment. PaymentID is bKash's own ID, unique per checkout session.
type BkashTransaction struct {
	Base
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	PlanID uuid.UUID `gorm:"type:uuid;not null" json:"plan_id"`
	// Plan is preloaded (not stored) so the transaction history endpoint can
	// show the plan title without a separate lookup per row.
	Plan SubscriptionPlan `gorm:"foreignKey:PlanID;references:ID" json:"plan"`
	// PaymentID is bKash's paymentID for this checkout session.
	PaymentID string                 `gorm:"uniqueIndex;not null" json:"payment_id"`
	TrxID     string                 `json:"trx_id"`
	Amount    int                    `gorm:"not null" json:"amount"`
	// OriginalAmount is the full plan price before any coupon discount —
	// stored separately so the transaction history always shows both the
	// charged amount and what the plan normally costs.
	OriginalAmount int                    `gorm:"not null;default:0" json:"original_amount"`
	CouponCodeID   *uuid.UUID             `gorm:"type:uuid;index" json:"coupon_code_id,omitempty"`
	// CouponReserved is true while this transaction holds one use of its
	// coupon (taken atomically at checkout, released if the payment dies).
	CouponReserved bool `gorm:"not null;default:false" json:"-"`
	// SubscriptionID is the UserSubscription this payment produced — lets a
	// repeated execute return the same subscription instead of guessing.
	SubscriptionID *uuid.UUID `gorm:"type:uuid;index" json:"subscription_id,omitempty"`
	Status         BkashTransactionStatus `gorm:"type:varchar(20);default:'initiated'" json:"status"`
	// RawResponse is the last bKash JSON response seen for this transaction —
	// kept for support/debugging, never parsed back out.
	RawResponse string `gorm:"type:text" json:"-"`
}

type BkashTransactionRepository interface {
	Create(ctx context.Context, tx *BkashTransaction) error
	GetByPaymentID(ctx context.Context, paymentID string) (*BkashTransaction, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status BkashTransactionStatus, trxID, rawResponse string) error
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]BkashTransaction, int64, error)
}
