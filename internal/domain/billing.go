package domain

import (
	"time"

	"github.com/google/uuid"
)

// All money in this file is whole BDT (taka) — the same unit that is sent to
// bKash (see pkg/bkash CreatePayment) and stored in SubscriptionPlan.Price
// and Order.TotalAmount. (Product.Price's "poisha" comment is stale: the
// value is charged to bKash as-is, so it is taka.)

// Journal is one balanced accounting event. Its LedgerEntry rows always sum
// to debit == credit (enforced by service.postJournal). The unique
// (kind, source_type, source_id) index is the idempotency key: posting the
// same business event twice is a no-op instead of double-counting revenue.
type Journal struct {
	Base
	Kind       string    `gorm:"size:40;not null;uniqueIndex:idx_journal_source,priority:1" json:"kind"`
	SourceType string    `gorm:"size:30;not null;uniqueIndex:idx_journal_source,priority:2" json:"source_type"`
	SourceID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_journal_source,priority:3" json:"source_id"`
	Memo       string    `gorm:"size:255" json:"memo"`

	Entries []LedgerEntry `gorm:"foreignKey:JournalID" json:"entries,omitempty"`
}

// LedgerEntry is a single debit or credit against a named account. Rows are
// append-only: corrections are made with a new, opposite journal.
type LedgerEntry struct {
	Base
	JournalID uuid.UUID `gorm:"type:uuid;not null;index" json:"journal_id"`
	Account   string    `gorm:"size:120;not null;index" json:"account"`
	Debit     int       `gorm:"not null;default:0" json:"debit"`
	Credit    int       `gorm:"not null;default:0" json:"credit"`
}

// Invoice is the customer-facing receipt for a completed payment. Customer
// details and line items are snapshots so later profile/plan edits never
// rewrite history.
type Invoice struct {
	Base
	Number        string    `gorm:"size:30;uniqueIndex;not null" json:"number"` // INV-2026-000123
	UserID        uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Kind          string    `gorm:"size:20;not null" json:"kind"` // subscription | order
	SourceType    string    `gorm:"size:30;not null;uniqueIndex:idx_invoice_source,priority:1" json:"source_type"`
	SourceID      uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_invoice_source,priority:2" json:"source_id"`
	CustomerName  string    `gorm:"size:255" json:"customer_name"`
	CustomerEmail string    `gorm:"size:255" json:"customer_email"`
	CustomerPhone string    `gorm:"size:20" json:"customer_phone"`
	Currency      string    `gorm:"size:3;not null;default:'BDT'" json:"currency"`
	Subtotal      int       `gorm:"not null" json:"subtotal"`
	Discount      int       `gorm:"not null;default:0" json:"discount"`
	Total         int       `gorm:"not null" json:"total"`
	PaymentMethod string    `gorm:"size:30" json:"payment_method"`
	PaymentRef    string    `gorm:"size:100" json:"payment_ref"` // bKash trxID
	IssuedAt      time.Time `gorm:"not null" json:"issued_at"`
	// VoidedAt is set when the payment was refunded; the printable invoice
	// is stamped VOID.
	VoidedAt *time.Time    `json:"voided_at,omitempty"`
	Lines    []InvoiceLine `gorm:"foreignKey:InvoiceID" json:"lines"`
}

type InvoiceLine struct {
	Base
	InvoiceID   uuid.UUID `gorm:"type:uuid;not null;index" json:"invoice_id"`
	Description string    `gorm:"size:255;not null" json:"description"`
	Quantity    int       `gorm:"not null;default:1" json:"quantity"`
	UnitPrice   int       `gorm:"not null" json:"unit_price"`
	Total       int       `gorm:"not null" json:"total"`
}

// InvoiceCounter hands out gap-free sequential invoice numbers per year. The
// row is incremented with a single upsert inside the fulfilment transaction,
// so a rolled-back payment never burns a number.
type InvoiceCounter struct {
	Year int `gorm:"primaryKey;autoIncrement:false"`
	Last int `gorm:"not null;default:0"`
}

// Refund records money returned to a payer. The bKash transfer itself is made
// by an admin in the bKash merchant portal (Reference is its transaction id);
// this row, the ledger reversal and the state changes are the system's record.
type Refund struct {
	Base
	Kind       string    `gorm:"size:20;not null" json:"kind"` // subscription | order
	SourceType string    `gorm:"size:30;not null;uniqueIndex:idx_refund_source,priority:1" json:"source_type"`
	SourceID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_refund_source,priority:2" json:"source_id"`
	UserID     uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Amount     int       `gorm:"not null" json:"amount"`
	Reference  string    `gorm:"size:100" json:"reference"`
	Reason     string    `gorm:"size:500" json:"reason"`
	RefundedBy uuid.UUID `gorm:"type:uuid" json:"refunded_by"`
}

type PayoutStatus string

const (
	PayoutRequested PayoutStatus = "requested"
	PayoutPaid      PayoutStatus = "paid"
	PayoutRejected  PayoutStatus = "rejected"
)

// MerchantPayout is a request to move a merchant's accrued balance out to
// their payout account. The amount is deducted from the merchant's ledger
// balance the moment it is requested (so it can't be requested twice) and is
// either settled (paid) or returned (rejected) by an admin.
type MerchantPayout struct {
	Base
	MerchantID uuid.UUID    `gorm:"type:uuid;not null;index" json:"merchant_id"`
	Amount     int          `gorm:"not null" json:"amount"`
	Status     PayoutStatus `gorm:"type:varchar(20);not null;default:'requested';index" json:"status"`
	// Method/Account are snapshotted from the merchant at request time.
	Method      string     `gorm:"size:20" json:"method"`
	Account     string     `gorm:"size:255" json:"account"`
	Reference   string     `gorm:"size:100" json:"reference"` // transfer/trx id entered by the admin when paying
	Note        string     `gorm:"size:500" json:"note"`
	RequestedBy uuid.UUID  `gorm:"type:uuid" json:"requested_by"`
	ProcessedBy *uuid.UUID `gorm:"type:uuid" json:"processed_by,omitempty"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}
