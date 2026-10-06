package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/logger"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOrderNotDelivered   = errors.New("order is not delivered")
	ErrInsufficientBalance = errors.New("amount exceeds the merchant's available balance")
	ErrBelowMinPayout      = errors.New("amount is below the minimum payout")
	ErrNoPayoutAccount     = errors.New("merchant has no payout method/account set")
	ErrMerchantNotApproved = errors.New("merchant is not approved")
	ErrMerchantNotFound    = errors.New("merchant not found")
	ErrPayoutNotFound      = errors.New("payout not found")
	ErrPayoutNotRequested  = errors.New("payout is not awaiting processing")
	ErrInvoiceNotFound     = errors.New("invoice not found")
	ErrLedgerUnbalanced    = errors.New("journal debits and credits do not balance")
	ErrInvalidPayoutAmount = errors.New("payout amount must be positive")
)

// Ledger accounts. Convention: balances are credit − debit for the
// liability: and revenue: accounts and debit − credit for asset: accounts.
const (
	AccGatewayBkash      = "asset:gateway_bkash"       // money bKash is holding for us
	AccCashOut           = "asset:cash_out"            // credited with every transfer to a merchant (cumulative paid out)
	AccOrdersEscrow      = "liability:orders_escrow"   // buyer money for orders not yet delivered
	AccPayoutsPending    = "liability:payouts_pending" // requested payouts not yet transferred
	AccRevenueSubs       = "revenue:subscriptions"
	AccRevenueCommission = "revenue:commission"
	AccRevenuePlatform   = "revenue:platform_sales" // sales of the platform's own products
	accMerchantPrefix    = "liability:merchant:"    // + merchant id: what we owe a merchant
)

// MerchantAccount is the ledger account holding what the platform owes a merchant.
func MerchantAccount(merchantID uuid.UUID) string { return accMerchantPrefix + merchantID.String() }

// Journal kinds (the idempotency key together with source type/id).
const (
	KindSubscriptionPayment = "subscription_payment"
	KindOrderPayment        = "order_payment"
	KindOrderRelease        = "order_release"
	KindPayoutRequest       = "payout_request"
	KindPayoutPaid          = "payout_paid"
	KindPayoutRejected      = "payout_rejected"
)

// DefaultMinPayout is the smallest payout a merchant may request (BDT).
const DefaultMinPayout = 500

// BillingService owns the ledger, invoices and merchant payouts. Every
// mutating method that is part of a payment takes the caller's *gorm.DB
// transaction handle, so ledger rows, the invoice and the payment state
// change commit or roll back together. A nil *BillingService is valid and
// does nothing, so payment services can be built without billing in tests.
type BillingService struct {
	db        *gorm.DB
	MinPayout int
	notifier  *OrderNotifier // optional; set by SetOrderNotifier
}

// SetOrderNotifier enables the buyer notification when an order is refunded.
func (b *BillingService) SetOrderNotifier(n *OrderNotifier) { b.notifier = n }

func NewBillingService(db *gorm.DB) *BillingService {
	return &BillingService{db: db, MinPayout: DefaultMinPayout}
}

// ─── Ledger primitives ───────────────────────────────────────────────────

type journalLine struct {
	Account       string
	Debit, Credit int
}

// postJournal writes one balanced journal. It is idempotent on
// (kind, source_type, source_id): a repeat returns created=false and writes
// nothing. Callers hold a row lock on the source, so two concurrent posts
// cannot both pass the existence check.
func postJournal(tx *gorm.DB, kind, sourceType string, sourceID uuid.UUID, memo string, lines []journalLine) (bool, error) {
	var debit, credit int
	for _, l := range lines {
		if l.Debit < 0 || l.Credit < 0 || (l.Debit > 0 && l.Credit > 0) {
			return false, fmt.Errorf("ledger: invalid line for %s: debit=%d credit=%d", l.Account, l.Debit, l.Credit)
		}
		debit += l.Debit
		credit += l.Credit
	}
	if debit != credit {
		return false, fmt.Errorf("%w: %s %s/%s debit=%d credit=%d", ErrLedgerUnbalanced, kind, sourceType, sourceID, debit, credit)
	}

	var existing int64
	if err := tx.Model(&domain.Journal{}).
		Where("kind = ? AND source_type = ? AND source_id = ?", kind, sourceType, sourceID).
		Count(&existing).Error; err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}

	j := &domain.Journal{Kind: kind, SourceType: sourceType, SourceID: sourceID, Memo: memo}
	if err := tx.Create(j).Error; err != nil {
		return false, err
	}

	var entries []domain.LedgerEntry
	for _, l := range lines {
		if l.Debit == 0 && l.Credit == 0 {
			continue
		}
		entries = append(entries, domain.LedgerEntry{JournalID: j.ID, Account: l.Account, Debit: l.Debit, Credit: l.Credit})
	}
	if len(entries) > 0 {
		if err := tx.Create(&entries).Error; err != nil {
			return false, err
		}
	}
	return true, nil
}

func journalExists(tx *gorm.DB, kind, sourceType string, sourceID uuid.UUID) (bool, error) {
	var n int64
	err := tx.Model(&domain.Journal{}).
		Where("kind = ? AND source_type = ? AND source_id = ?", kind, sourceType, sourceID).
		Count(&n).Error
	return n > 0, err
}

// creditBalance is credit − debit for an account (the liability/revenue sign).
func creditBalance(tx *gorm.DB, account string) (int, error) {
	var bal int
	err := tx.Model(&domain.LedgerEntry{}).
		Where("account = ?", account).
		Select("COALESCE(SUM(credit), 0) - COALESCE(SUM(debit), 0)").
		Scan(&bal).Error
	return bal, err
}

// MerchantBalance is what the platform currently owes a merchant: delivered
// sales net of commission, minus payouts requested or paid. It can be
// negative when commission owed on cash-on-delivery sales exceeds earnings.
func (b *BillingService) MerchantBalance(ctx context.Context, merchantID uuid.UUID) (int, error) {
	return creditBalance(b.db.WithContext(ctx), MerchantAccount(merchantID))
}

// ─── Invoices ────────────────────────────────────────────────────────────

type invoiceSpec struct {
	UserID        uuid.UUID
	Kind          string // subscription | order
	SourceType    string
	SourceID      uuid.UUID
	PaymentMethod string
	PaymentRef    string
	Subtotal      int
	Discount      int
	Total         int
	Lines         []domain.InvoiceLine
}

func nextInvoiceNumber(tx *gorm.DB, now time.Time) (string, error) {
	year := now.UTC().Year()
	// One upsert both creates the year's counter and increments it; in
	// Postgres the UPDATE holds the row lock until the surrounding
	// transaction ends, so concurrent payments queue instead of colliding.
	err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "year"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"last": gorm.Expr("invoice_counters.last + 1")}),
	}).Create(&domain.InvoiceCounter{Year: year, Last: 1}).Error
	if err != nil {
		return "", err
	}
	var c domain.InvoiceCounter
	if err := tx.First(&c, "year = ?", year).Error; err != nil {
		return "", err
	}
	return fmt.Sprintf("INV-%d-%06d", year, c.Last), nil
}

// issueInvoice creates the receipt for a payment, once. A repeat for the same
// source returns the existing invoice without consuming a number.
func issueInvoice(tx *gorm.DB, spec invoiceSpec) (*domain.Invoice, error) {
	var existing domain.Invoice
	err := tx.Preload("Lines").First(&existing, "source_type = ? AND source_id = ?", spec.SourceType, spec.SourceID).Error
	if err == nil {
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var user domain.User
	if err := tx.Unscoped().First(&user, "id = ?", spec.UserID).Error; err != nil {
		return nil, fmt.Errorf("invoice: load customer: %w", err)
	}

	now := time.Now()
	number, err := nextInvoiceNumber(tx, now)
	if err != nil {
		return nil, err
	}
	inv := &domain.Invoice{
		Number:        number,
		UserID:        spec.UserID,
		Kind:          spec.Kind,
		SourceType:    spec.SourceType,
		SourceID:      spec.SourceID,
		CustomerName:  strings.TrimSpace(user.FullName()),
		CustomerEmail: user.Email,
		CustomerPhone: user.Phone,
		Currency:      "BDT",
		Subtotal:      spec.Subtotal,
		Discount:      spec.Discount,
		Total:         spec.Total,
		PaymentMethod: spec.PaymentMethod,
		PaymentRef:    spec.PaymentRef,
		IssuedAt:      now,
	}
	if err := tx.Create(inv).Error; err != nil {
		return nil, err
	}
	for i := range spec.Lines {
		spec.Lines[i].InvoiceID = inv.ID
	}
	if len(spec.Lines) > 0 {
		if err := tx.Create(&spec.Lines).Error; err != nil {
			return nil, err
		}
	}
	inv.Lines = spec.Lines
	return inv, nil
}

// ─── Hooks called from payment fulfilment (inside the caller's transaction) ──

// recordSubscriptionPayment books a completed subscription payment as
// revenue and issues its invoice.
func (b *BillingService) recordSubscriptionPayment(tx *gorm.DB, bt *domain.BkashTransaction, planTitle string) error {
	if b == nil {
		return nil
	}
	if _, err := postJournal(tx, KindSubscriptionPayment, "bkash_transaction", bt.ID, "Subscription: "+planTitle, []journalLine{
		{Account: AccGatewayBkash, Debit: bt.Amount},
		{Account: AccRevenueSubs, Credit: bt.Amount},
	}); err != nil {
		return err
	}
	original := bt.OriginalAmount
	if original < bt.Amount {
		original = bt.Amount
	}
	_, err := issueInvoice(tx, invoiceSpec{
		UserID:        bt.UserID,
		Kind:          "subscription",
		SourceType:    "bkash_transaction",
		SourceID:      bt.ID,
		PaymentMethod: "bkash",
		PaymentRef:    bt.TrxID,
		Subtotal:      original,
		Discount:      original - bt.Amount,
		Total:         bt.Amount,
		Lines:         []domain.InvoiceLine{{Description: planTitle, Quantity: 1, UnitPrice: original, Total: original}},
	})
	return err
}

// recordOrderPayment books buyer money for a paid marketplace order into
// escrow (it becomes merchant balance only once the order is delivered) and
// issues the invoice.
func (b *BillingService) recordOrderPayment(tx *gorm.DB, order *domain.Order, trxID string) error {
	if b == nil {
		return nil
	}
	if _, err := postJournal(tx, KindOrderPayment, "order", order.ID, "Order payment", []journalLine{
		{Account: AccGatewayBkash, Debit: order.TotalAmount},
		{Account: AccOrdersEscrow, Credit: order.TotalAmount},
	}); err != nil {
		return err
	}
	return b.issueOrderInvoice(tx, order, string(domain.PaymentMethodBkash), trxID)
}

func (b *BillingService) issueOrderInvoice(tx *gorm.DB, order *domain.Order, method, ref string) error {
	items := order.Items
	if len(items) == 0 {
		if err := tx.Where("order_id = ?", order.ID).Find(&items).Error; err != nil {
			return err
		}
	}
	lines := make([]domain.InvoiceLine, 0, len(items))
	subtotal := 0
	for _, it := range items {
		total := it.UnitPrice * it.Quantity
		subtotal += total
		lines = append(lines, domain.InvoiceLine{Description: it.ProductTitle, Quantity: it.Quantity, UnitPrice: it.UnitPrice, Total: total})
	}
	_, err := issueInvoice(tx, invoiceSpec{
		UserID: order.BuyerID, Kind: "order", SourceType: "order", SourceID: order.ID,
		PaymentMethod: method, PaymentRef: ref,
		Subtotal: subtotal, Total: subtotal, Lines: lines,
	})
	return err
}

// ─── Order delivery → merchant earnings ──────────────────────────────────

func commissionFor(gross int, ratePercent float64) int {
	c := int(math.Round(float64(gross) * ratePercent / 100))
	if c < 0 {
		return 0
	}
	if c > gross {
		return gross
	}
	return c
}

// OnOrderDelivered turns a delivered order into merchant earnings:
//
//   - bKash orders: buyer money leaves escrow; each merchant is credited the
//     sale net of the commission rate snapshotted at checkout, the platform
//     books the commission as revenue (and the platform's own products as
//     platform sales).
//   - cash-on-delivery orders: the merchant already holds the cash, so only
//     the commission is booked — debited from the merchant's balance (it can
//     go negative and is netted against future earnings).
//
// Idempotent and safe to retry: the release journal is keyed on the order.
func (b *BillingService) OnOrderDelivered(ctx context.Context, orderID uuid.UUID) error {
	if b == nil {
		return nil
	}
	return b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order domain.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", orderID).Error; err != nil {
			return err
		}
		if order.Status != domain.OrderStatusDelivered {
			return ErrOrderNotDelivered
		}
		if done, err := journalExists(tx, KindOrderRelease, "order", order.ID); err != nil || done {
			return err
		}
		if err := tx.Where("order_id = ?", order.ID).Find(&order.Items).Error; err != nil {
			return err
		}

		isBkash := order.PaymentMethod == domain.PaymentMethodBkash
		if isBkash {
			// Lazily backfill the payment journal for orders paid before the
			// ledger existed, so escrow is never debited without a credit.
			paid, err := journalExists(tx, KindOrderPayment, "order", order.ID)
			if err != nil {
				return err
			}
			if !paid {
				var ot domain.OrderTransaction
				_ = tx.Where("order_id = ? AND status = ?", order.ID, domain.BkashStatusCompleted).First(&ot).Error
				if err := b.recordOrderPayment(tx, &order, ot.TrxID); err != nil {
					return err
				}
			}
		} else if err := b.issueOrderInvoice(tx, &order, string(domain.PaymentMethodCashOnDelivery), ""); err != nil {
			return err
		}

		type share struct{ gross, commission int }
		shares := map[uuid.UUID]*share{}
		var merchantIDs []uuid.UUID
		for _, it := range order.Items {
			s, ok := shares[it.MerchantID]
			if !ok {
				s = &share{}
				shares[it.MerchantID] = s
				merchantIDs = append(merchantIDs, it.MerchantID)
			}
			gross := it.UnitPrice * it.Quantity
			s.gross += gross
			s.commission += commissionFor(gross, it.CommissionRateSnapshot)
		}
		sort.Slice(merchantIDs, func(i, k int) bool { return merchantIDs[i].String() < merchantIDs[k].String() })

		platform := map[uuid.UUID]bool{}
		var merchants []domain.Merchant
		if err := tx.Unscoped().Where("id IN ?", merchantIDs).Find(&merchants).Error; err != nil {
			return err
		}
		for _, m := range merchants {
			platform[m.ID] = m.IsPlatform
		}

		var lines []journalLine
		total := 0
		for _, id := range merchantIDs {
			s := shares[id]
			total += s.gross
			switch {
			case platform[id]:
				if isBkash {
					lines = append(lines, journalLine{Account: AccRevenuePlatform, Credit: s.gross})
				}
			case isBkash:
				lines = append(lines, journalLine{Account: MerchantAccount(id), Credit: s.gross - s.commission})
				lines = append(lines, journalLine{Account: AccRevenueCommission, Credit: s.commission})
			default: // cash on delivery
				lines = append(lines, journalLine{Account: MerchantAccount(id), Debit: s.commission})
				lines = append(lines, journalLine{Account: AccRevenueCommission, Credit: s.commission})
			}
		}
		if isBkash {
			lines = append(lines, journalLine{Account: AccOrdersEscrow, Debit: total})
		}
		_, err := postJournal(tx, KindOrderRelease, "order", order.ID, "Order delivered", lines)
		return err
	})
}

// ReleaseDeliveredOrders is the safety net for OnOrderDelivered: it finds
// delivered orders whose earnings were never booked (the status change
// committed but the follow-up call failed, or the order predates the ledger)
// and books them. Returns how many orders were processed.
func (b *BillingService) ReleaseDeliveredOrders(ctx context.Context, limit int) (int, error) {
	var ids []uuid.UUID
	err := b.db.WithContext(ctx).Model(&domain.Order{}).
		Where("status = ?", domain.OrderStatusDelivered).
		Where("NOT EXISTS (SELECT 1 FROM journals j WHERE j.source_type = 'order' AND j.source_id = orders.id AND j.kind = ?)", KindOrderRelease).
		Limit(limit).Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if err := b.OnOrderDelivered(ctx, id); err != nil {
			logger.Errorf("[billing] release order %s: %v", id, err)
			continue
		}
		done++
	}
	return done, nil
}

// ─── Merchant payouts ────────────────────────────────────────────────────

// RequestPayout reserves amount from the merchant's balance and records a
// payout for an admin to transfer. The balance check and the deduction happen
// under a row lock on the merchant, so concurrent requests cannot overdraw.
func (b *BillingService) RequestPayout(ctx context.Context, merchantID, requestedBy uuid.UUID, amount int) (*domain.MerchantPayout, error) {
	if amount <= 0 {
		return nil, ErrInvalidPayoutAmount
	}
	if amount < b.MinPayout {
		return nil, fmt.Errorf("%w (minimum %d BDT)", ErrBelowMinPayout, b.MinPayout)
	}
	var payout *domain.MerchantPayout
	err := b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m domain.Merchant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, "id = ?", merchantID).Error; err != nil {
			return ErrMerchantNotFound
		}
		if m.Status != domain.MerchantStatusApproved || m.IsPlatform {
			return ErrMerchantNotApproved
		}
		if m.PayoutMethod == "" || m.PayoutAccount == "" {
			return ErrNoPayoutAccount
		}
		bal, err := creditBalance(tx, MerchantAccount(merchantID))
		if err != nil {
			return err
		}
		if amount > bal {
			return ErrInsufficientBalance
		}
		p := &domain.MerchantPayout{
			MerchantID: merchantID, Amount: amount, Status: domain.PayoutRequested,
			Method: m.PayoutMethod, Account: m.PayoutAccount, RequestedBy: requestedBy,
		}
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		if _, err := postJournal(tx, KindPayoutRequest, "payout", p.ID, "Payout requested", []journalLine{
			{Account: MerchantAccount(merchantID), Debit: amount},
			{Account: AccPayoutsPending, Credit: amount},
		}); err != nil {
			return err
		}
		payout = p
		return nil
	})
	return payout, err
}

func (b *BillingService) settlePayout(ctx context.Context, payoutID, adminID uuid.UUID, to domain.PayoutStatus, reference, note string) (*domain.MerchantPayout, error) {
	var out *domain.MerchantPayout
	err := b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p domain.MerchantPayout
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", payoutID).Error; err != nil {
			return ErrPayoutNotFound
		}
		if p.Status != domain.PayoutRequested {
			return ErrPayoutNotRequested
		}
		now := time.Now()
		p.Status, p.Reference, p.ProcessedBy, p.ProcessedAt = to, reference, &adminID, &now
		if note != "" {
			p.Note = note
		}
		if err := tx.Save(&p).Error; err != nil {
			return err
		}

		var lines []journalLine
		kind, memo := KindPayoutPaid, "Payout transferred"
		if to == domain.PayoutPaid {
			lines = []journalLine{{Account: AccPayoutsPending, Debit: p.Amount}, {Account: AccCashOut, Credit: p.Amount}}
		} else {
			kind, memo = KindPayoutRejected, "Payout rejected, balance returned"
			lines = []journalLine{{Account: AccPayoutsPending, Debit: p.Amount}, {Account: MerchantAccount(p.MerchantID), Credit: p.Amount}}
		}
		if _, err := postJournal(tx, kind, "payout", p.ID, memo, lines); err != nil {
			return err
		}
		out = &p
		return nil
	})
	return out, err
}

// MarkPayoutPaid records that an admin transferred the money. reference is
// the transfer/transaction id kept for the merchant and for audits.
func (b *BillingService) MarkPayoutPaid(ctx context.Context, payoutID, adminID uuid.UUID, reference string) (*domain.MerchantPayout, error) {
	return b.settlePayout(ctx, payoutID, adminID, domain.PayoutPaid, reference, "")
}

// RejectPayout cancels a requested payout and returns the amount to the
// merchant's balance.
func (b *BillingService) RejectPayout(ctx context.Context, payoutID, adminID uuid.UUID, note string) (*domain.MerchantPayout, error) {
	return b.settlePayout(ctx, payoutID, adminID, domain.PayoutRejected, "", note)
}

func (b *BillingService) ListPayouts(ctx context.Context, merchantID *uuid.UUID, status domain.PayoutStatus, limit, offset int) ([]domain.MerchantPayout, int64, error) {
	q := b.db.WithContext(ctx).Model(&domain.MerchantPayout{})
	if merchantID != nil {
		q = q.Where("merchant_id = ?", *merchantID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.MerchantPayout
	err := q.Order("created_at desc").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

// ─── Invoice reads ───────────────────────────────────────────────────────

// GetInvoice returns an invoice; when userID is non-nil it must belong to
// that user (admins pass nil).
func (b *BillingService) GetInvoice(ctx context.Context, id uuid.UUID, userID *uuid.UUID) (*domain.Invoice, error) {
	q := b.db.WithContext(ctx).Preload("Lines").Where("id = ?", id)
	if userID != nil {
		q = q.Where("user_id = ?", *userID)
	}
	var inv domain.Invoice
	if err := q.First(&inv).Error; err != nil {
		return nil, ErrInvoiceNotFound
	}
	return &inv, nil
}

func (b *BillingService) ListInvoices(ctx context.Context, userID *uuid.UUID, limit, offset int) ([]domain.Invoice, int64, error) {
	q := b.db.WithContext(ctx).Model(&domain.Invoice{})
	if userID != nil {
		q = q.Where("user_id = ?", *userID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Invoice
	err := q.Preload("Lines").Order("issued_at desc").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, total, err
}

// ─── Admin reporting ─────────────────────────────────────────────────────

type BillingSummary struct {
	SubscriptionRevenue int `json:"subscription_revenue"`
	CommissionRevenue   int `json:"commission_revenue"`
	PlatformSales       int `json:"platform_sales"`
	TotalRevenue        int `json:"total_revenue"`
	MerchantPayable     int `json:"merchant_payable"` // owed to merchants, net of everything already requested/paid
	PayoutsPending      int `json:"payouts_pending"`  // requested, not yet transferred
	PaidOut             int `json:"paid_out"`         // transferred to merchants
	OrdersEscrow        int `json:"orders_escrow"`    // buyer money for undelivered orders
	GatewayHeld         int `json:"gateway_held"`     // received through bKash overall
}

func (b *BillingService) Summary(ctx context.Context) (*BillingSummary, error) {
	var rows []struct {
		Account string
		Debit   int
		Credit  int
	}
	if err := b.db.WithContext(ctx).Model(&domain.LedgerEntry{}).
		Select("account, COALESCE(SUM(debit),0) AS debit, COALESCE(SUM(credit),0) AS credit").
		Group("account").Scan(&rows).Error; err != nil {
		return nil, err
	}
	s := &BillingSummary{}
	for _, r := range rows {
		credit := r.Credit - r.Debit
		switch {
		case r.Account == AccRevenueSubs:
			s.SubscriptionRevenue = credit
		case r.Account == AccRevenueCommission:
			s.CommissionRevenue = credit
		case r.Account == AccRevenuePlatform:
			s.PlatformSales = credit
		case r.Account == AccPayoutsPending:
			s.PayoutsPending = credit
		case r.Account == AccCashOut:
			s.PaidOut = credit // credit-only account: running total of transfers sent
		case r.Account == AccOrdersEscrow:
			s.OrdersEscrow = credit
		case r.Account == AccGatewayBkash:
			s.GatewayHeld = -credit
		case strings.HasPrefix(r.Account, accMerchantPrefix):
			s.MerchantPayable += credit
		}
	}
	s.TotalRevenue = s.SubscriptionRevenue + s.CommissionRevenue + s.PlatformSales
	return s, nil
}

type LedgerRow struct {
	ID         uuid.UUID `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Account    string    `json:"account"`
	Debit      int       `json:"debit"`
	Credit     int       `json:"credit"`
	Kind       string    `json:"kind"`
	SourceType string    `json:"source_type"`
	SourceID   uuid.UUID `json:"source_id"`
	Memo       string    `json:"memo"`
}

// ListLedger returns ledger entries newest first, optionally for one account.
func (b *BillingService) ListLedger(ctx context.Context, account string, limit, offset int) ([]LedgerRow, error) {
	q := b.db.WithContext(ctx).Table("ledger_entries AS e").
		Select("e.id, e.created_at, e.account, e.debit, e.credit, j.kind, j.source_type, j.source_id, j.memo").
		Joins("JOIN journals j ON j.id = e.journal_id")
	if account != "" {
		q = q.Where("e.account = ?", account)
	}
	var rows []LedgerRow
	err := q.Order("e.created_at desc").Limit(limit).Offset(offset).Scan(&rows).Error
	return rows, err
}
