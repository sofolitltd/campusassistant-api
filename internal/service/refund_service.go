package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotRefundable   = errors.New("this payment cannot be refunded")
	ErrAlreadyRefunded = errors.New("this payment has already been refunded")
)

// reverseJournals posts the exact opposite of every journal of the given
// kinds for a source. Idempotent (each reversal is keyed "refund:<kind>").
// Journals that were never posted (e.g. a payment flagged for review) are
// simply skipped, so refunding those is a pure state change.
func reverseJournals(tx *gorm.DB, sourceType string, sourceID uuid.UUID, kinds ...string) error {
	var journals []domain.Journal
	if err := tx.Where("source_type = ? AND source_id = ? AND kind IN ?", sourceType, sourceID, kinds).Find(&journals).Error; err != nil {
		return err
	}
	for _, j := range journals {
		var entries []domain.LedgerEntry
		if err := tx.Where("journal_id = ?", j.ID).Find(&entries).Error; err != nil {
			return err
		}
		lines := make([]journalLine, 0, len(entries))
		for _, e := range entries {
			lines = append(lines, journalLine{Account: e.Account, Debit: e.Credit, Credit: e.Debit}) // swapped
		}
		if _, err := postJournal(tx, "refund:"+j.Kind, sourceType, sourceID, "Refund of "+j.Kind, lines); err != nil {
			return err
		}
	}
	return nil
}

// recomputePro derives the user's Pro state from their non-refunded,
// still-running subscriptions (a later stacked purchase keeps its own end
// date, so refunding an earlier one does not cut it short).
func recomputePro(tx *gorm.DB, userID uuid.UUID) error {
	var subs []domain.UserSubscription
	if err := tx.Where("user_id = ? AND refunded_at IS NULL AND (end_date IS NULL OR end_date > ?)", userID, time.Now()).Find(&subs).Error; err != nil {
		return err
	}
	isPro, lifetime := len(subs) > 0, false
	var latest *time.Time
	for _, s := range subs {
		if s.EndDate == nil {
			lifetime = true
		} else if latest == nil || s.EndDate.After(*latest) {
			e := *s.EndDate
			latest = &e
		}
	}
	var expiry *time.Time
	if isPro && !lifetime {
		expiry = latest
	}
	return tx.Model(&domain.User{}).Where("id = ?", userID).Updates(map[string]interface{}{"is_pro": isPro, "pro_expiry": expiry}).Error
}

func validateRefundInput(reference, reason string) (string, string, error) {
	reference, reason = strings.TrimSpace(reference), strings.TrimSpace(reason)
	if reference == "" {
		return "", "", fmt.Errorf("%w: the bKash refund transaction id is required", ErrNotRefundable)
	}
	return reference, reason, nil
}

// RefundSubscriptionPayment records that a subscription payment was refunded:
// reverses its revenue entry, voids the invoice, ends the subscription and
// recomputes the user's Pro status. It works for completed payments and for
// ones flagged needs_review (money moved but nothing was granted — a pure
// status change). The bKash transfer itself is done by an admin in the bKash
// portal; reference is its transaction id.
func (b *BillingService) RefundSubscriptionPayment(ctx context.Context, adminID, bkashTxID uuid.UUID, reference, reason string) (*domain.Refund, error) {
	reference, reason, err := validateRefundInput(reference, reason)
	if err != nil {
		return nil, err
	}
	var out *domain.Refund
	err = b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bt domain.BkashTransaction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&bt, "id = ?", bkashTxID).Error; err != nil {
			return ErrTransactionNotFound
		}
		switch bt.Status {
		case domain.BkashStatusRefunded:
			return ErrAlreadyRefunded
		case domain.BkashStatusCompleted, domain.BkashStatusNeedsReview:
		default:
			return fmt.Errorf("%w: payment is %s", ErrNotRefundable, bt.Status)
		}

		if err := reverseJournals(tx, "bkash_transaction", bt.ID, KindSubscriptionPayment); err != nil {
			return err
		}
		now := time.Now()
		if bt.SubscriptionID != nil {
			if err := tx.Model(&domain.UserSubscription{}).Where("id = ?", *bt.SubscriptionID).Update("refunded_at", now).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&domain.Invoice{}).Where("source_type = ? AND source_id = ?", "bkash_transaction", bt.ID).Update("voided_at", now).Error; err != nil {
			return err
		}
		if err := recomputePro(tx, bt.UserID); err != nil {
			return err
		}
		// A refunded payment gives its coupon use back.
		if bt.CouponCodeID != nil && bt.Status == domain.BkashStatusCompleted {
			if err := releaseCoupon(tx, *bt.CouponCodeID); err != nil {
				return err
			}
		}
		if err := tx.Model(&domain.BkashTransaction{}).Where("id = ?", bt.ID).Update("status", domain.BkashStatusRefunded).Error; err != nil {
			return err
		}
		r := &domain.Refund{Kind: "subscription", SourceType: "bkash_transaction", SourceID: bt.ID, UserID: bt.UserID,
			Amount: bt.Amount, Reference: reference, Reason: reason, RefundedBy: adminID}
		if err := tx.Create(r).Error; err != nil {
			return err
		}
		out = r
		return nil
	})
	return out, err
}

// RefundOrder records a refund of a bKash-paid marketplace order: reverses the
// payment (and, if the order was already delivered, the merchant earnings and
// commission), voids the invoice and cancels the order. Merchant balances can
// go negative if they were already paid out — that is netted against future
// earnings. Cash-on-delivery orders have no bKash money to refund (cancel them
// instead).
func (b *BillingService) RefundOrder(ctx context.Context, adminID, orderID uuid.UUID, reference, reason string) (*domain.Refund, error) {
	reference, reason, err := validateRefundInput(reference, reason)
	if err != nil {
		return nil, err
	}
	var out *domain.Refund
	err = b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", orderID).Error; err != nil {
			return ErrOrderNotFound
		}
		if order.PaymentMethod != domain.PaymentMethodBkash {
			return fmt.Errorf("%w: cash-on-delivery orders have no bKash payment", ErrNotRefundable)
		}
		var ot domain.OrderTransaction
		err := tx.Where("order_id = ? AND status IN ?", order.ID, []domain.BkashTransactionStatus{domain.BkashStatusCompleted, domain.BkashStatusNeedsReview, domain.BkashStatusRefunded}).
			Order("created_at desc").First(&ot).Error
		if err != nil {
			return fmt.Errorf("%w: no completed bKash payment found", ErrNotRefundable)
		}
		if ot.Status == domain.BkashStatusRefunded {
			return ErrAlreadyRefunded
		}

		// Release first (it consumed the payment's escrow), then the payment.
		if err := reverseJournals(tx, "order", order.ID, KindOrderRelease, KindOrderPayment); err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&domain.Invoice{}).Where("source_type = ? AND source_id = ?", "order", order.ID).Update("voided_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.Order{}).Where("id = ?", order.ID).Update("status", domain.OrderStatusCancelled).Error; err != nil {
			return err
		}
		if order.Status != domain.OrderStatusDelivered { // a delivered order's goods are with the buyer
			if err := restockOrder(tx, order.ID); err != nil {
				return err
			}
		}
		if err := recordOrderEvent(tx, order.ID, domain.OrderStatusCancelled, adminID); err != nil {
			return err
		}
		if err := tx.Model(&domain.OrderTransaction{}).Where("id = ?", ot.ID).Update("status", domain.BkashStatusRefunded).Error; err != nil {
			return err
		}
		r := &domain.Refund{Kind: "order", SourceType: "order", SourceID: order.ID, UserID: order.BuyerID,
			Amount: ot.Amount, Reference: reference, Reason: reason, RefundedBy: adminID}
		if err := tx.Create(r).Error; err != nil {
			return err
		}
		out = r
		return nil
	})
	if err == nil && out != nil {
		b.notifier.OrderStatusChanged(ctx, orderID, domain.OrderStatusCancelled)
	}
	return out, err
}

func (b *BillingService) ListRefunds(ctx context.Context, limit, offset int) ([]domain.Refund, int64, error) {
	var total int64
	if err := b.db.WithContext(ctx).Model(&domain.Refund{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Refund
	err := b.db.WithContext(ctx).Order("created_at desc").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

// AdminPayment is one row in the admin payments list (either kind).
type AdminPayment struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"` // subscription | order
	PaymentID string     `json:"payment_id"`
	TrxID     string     `json:"trx_id"`
	Amount    int        `json:"amount"`
	Status    string     `json:"status"`
	UserID    uuid.UUID  `json:"user_id"`
	UserEmail string     `json:"user_email"`
	Title     string     `json:"title"` // plan title, or "Order <id>"
	OrderID   *uuid.UUID `json:"order_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ListPayments lists bKash payments of both kinds for the admin payments
// screen, newest first, optionally filtered by status (e.g. needs_review).
func (b *BillingService) ListPayments(ctx context.Context, status string, limit, offset int) ([]AdminPayment, error) {
	db := b.db.WithContext(ctx)
	var subs []AdminPayment
	q := db.Table("bkash_transactions AS t").
		Select("t.id, 'subscription' AS kind, t.payment_id, t.trx_id, t.amount, t.status, t.user_id, u.email AS user_email, p.title AS title, t.created_at").
		Joins("LEFT JOIN users u ON u.id = t.user_id").Joins("LEFT JOIN subscription_plans p ON p.id = t.plan_id")
	if status != "" {
		q = q.Where("t.status = ?", status)
	}
	if err := q.Order("t.created_at desc").Limit(limit + offset).Scan(&subs).Error; err != nil {
		return nil, err
	}
	var orders []AdminPayment
	q = db.Table("order_transactions AS t").
		Select("t.id, 'order' AS kind, t.payment_id, t.trx_id, t.amount, t.status, o.buyer_id AS user_id, u.email AS user_email, 'Marketplace order' AS title, t.order_id AS order_id, t.created_at").
		Joins("LEFT JOIN orders o ON o.id = t.order_id").Joins("LEFT JOIN users u ON u.id = o.buyer_id")
	if status != "" {
		q = q.Where("t.status = ?", status)
	}
	if err := q.Order("t.created_at desc").Limit(limit + offset).Scan(&orders).Error; err != nil {
		return nil, err
	}
	all := append(subs, orders...)
	for i := 1; i < len(all); i++ { // small lists: insertion sort, newest first
		for k := i; k > 0 && all[k].CreatedAt.After(all[k-1].CreatedAt); k-- {
			all[k], all[k-1] = all[k-1], all[k]
		}
	}
	if offset >= len(all) {
		return []AdminPayment{}, nil
	}
	all = all[offset:]
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
