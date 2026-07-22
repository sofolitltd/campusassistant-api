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
	Status    BkashTransactionStatus `gorm:"type:varchar(20);default:'initiated'" json:"status"`
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
