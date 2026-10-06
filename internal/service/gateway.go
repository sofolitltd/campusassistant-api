package service

import (
	"context"
	"strconv"

	"campusassistant-api/pkg/bkash"
)

// BkashGateway is the slice of the bKash client the payment services use.
// *bkash.Client satisfies it; tests substitute a fake so payment logic can be
// exercised without the network.
type BkashGateway interface {
	CreatePayment(ctx context.Context, amountBDT int, invoiceNumber, payerReference, callbackURL string) (*bkash.CreatePaymentResult, error)
	ExecutePayment(ctx context.Context, paymentID string) (*bkash.ExecutePaymentResult, error)
	QueryPayment(ctx context.Context, paymentID string) (*bkash.QueryPaymentResult, error)
	IsProduction() bool
}

var _ BkashGateway = (*bkash.Client)(nil)

// bkashCompleted is the only transactionStatus that means money moved.
const bkashCompleted = "Completed"

// paidAmount parses the amount bKash reports. ok is false when bKash omitted
// or garbled it, in which case the caller skips the equality check rather
// than rejecting a real payment over a formatting quirk.
func paidAmount(r *bkash.ExecutePaymentResult) (amount int, ok bool) {
	if r == nil || r.Amount == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(r.Amount, 64)
	if err != nil {
		return 0, false
	}
	return int(f + 0.5), true
}
