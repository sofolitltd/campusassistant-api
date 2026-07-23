package domain

import (
	"context"

	"github.com/google/uuid"
)

// OrderTransaction records a bKash payment attempt for a marketplace Order.
// Structurally parallel to BkashTransaction but references OrderID instead of
// PlanID, so the subscription payment path stays untouched.
type OrderTransaction struct {
	Base
	OrderID      uuid.UUID              `gorm:"type:uuid;not null;index" json:"order_id"`
	PaymentID    string                 `gorm:"uniqueIndex;not null" json:"payment_id"`
	TrxID        string                 `json:"trx_id"`
	Amount       int                    `gorm:"not null" json:"amount"`
	Status       BkashTransactionStatus `gorm:"type:varchar(20);default:'initiated'" json:"status"`
	RawResponse  string                 `gorm:"type:text" json:"-"`
}

// OrderTransactionRepository handles the lifecycle of marketplace payment
// transactions.
type OrderTransactionRepository interface {
	Create(ctx context.Context, tx *OrderTransaction) error
	GetByPaymentID(ctx context.Context, paymentID string) (*OrderTransaction, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status BkashTransactionStatus, trxID, rawResponse string) error
}
