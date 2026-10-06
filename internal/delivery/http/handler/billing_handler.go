package handler

import (
	"errors"
	"net/http"
	"strconv"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// BillingHandler serves invoices, merchant earnings/payouts and the admin
// ledger views. User-scoped routes (/my/...) enforce ownership here; the
// admin routes are mounted behind JWT + middleware.RequireAdmin.
type BillingHandler struct {
	billing      *service.BillingService
	merchantRepo domain.MerchantRepository
}

func NewBillingHandler(billing *service.BillingService, merchantRepo domain.MerchantRepository) *BillingHandler {
	return &BillingHandler{billing: billing, merchantRepo: merchantRepo}
}

func pageParams(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ─── Invoices ────────────────────────────────────────────────────────────

func (h *BillingHandler) ListMyInvoices(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	limit, offset := pageParams(c)
	rows, total, err := h.billing.ListInvoices(c.Request.Context(), &userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load invoices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"invoices": rows, "total": total})
}

func (h *BillingHandler) loadMyInvoice(c *gin.Context) (*domain.Invoice, bool) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid invoice id"})
		return nil, false
	}
	inv, err := h.billing.GetInvoice(c.Request.Context(), id, &userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invoice not found"})
		return nil, false
	}
	return inv, true
}

func (h *BillingHandler) GetMyInvoice(c *gin.Context) {
	if inv, ok := h.loadMyInvoice(c); ok {
		c.JSON(http.StatusOK, inv)
	}
}

// PrintMyInvoice returns a printable HTML invoice.
func (h *BillingHandler) PrintMyInvoice(c *gin.Context) {
	inv, ok := h.loadMyInvoice(c)
	if !ok {
		return
	}
	page, err := service.RenderInvoiceHTML(inv)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to render invoice"})
		return
	}
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

func (h *BillingHandler) AdminListInvoices(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.billing.ListInvoices(c.Request.Context(), nil, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load invoices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"invoices": rows, "total": total})
}

// ─── Merchant earnings & payouts (owner only) ────────────────────────────

// ownedMerchant resolves :id and confirms the caller owns that merchant.
func (h *BillingHandler) ownedMerchant(c *gin.Context) (*domain.Merchant, bool) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return nil, false
	}
	m, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return nil, false
	}
	if m.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return nil, false
	}
	return m, true
}

func (h *BillingHandler) MyMerchantEarnings(c *gin.Context) {
	m, ok := h.ownedMerchant(c)
	if !ok {
		return
	}
	bal, err := h.billing.MerchantBalance(c.Request.Context(), m.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load balance"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"balance":            bal,
		"min_payout":         h.billing.MinPayout,
		"has_payout_account": m.PayoutMethod != "" && m.PayoutAccount != "",
		"currency":           "BDT",
	})
}

func (h *BillingHandler) ListMyMerchantPayouts(c *gin.Context) {
	m, ok := h.ownedMerchant(c)
	if !ok {
		return
	}
	limit, offset := pageParams(c)
	rows, total, err := h.billing.ListPayouts(c.Request.Context(), &m.ID, "", limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load payouts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"payouts": rows, "total": total})
}

type requestPayoutBody struct {
	Amount int `json:"amount" binding:"required"`
}

func (h *BillingHandler) RequestMyMerchantPayout(c *gin.Context) {
	m, ok := h.ownedMerchant(c)
	if !ok {
		return
	}
	var req requestPayoutBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.MustGet("user_id").(uuid.UUID)
	p, err := h.billing.RequestPayout(c.Request.Context(), m.ID, userID, req.Amount)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidPayoutAmount), errors.Is(err, service.ErrBelowMinPayout), errors.Is(err, service.ErrNoPayoutAccount):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrInsufficientBalance):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrMerchantNotApproved):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to request payout"})
		}
		return
	}
	c.JSON(http.StatusCreated, p)
}

// ─── Admin ───────────────────────────────────────────────────────────────

func (h *BillingHandler) AdminSummary(c *gin.Context) {
	s, err := h.billing.Summary(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build summary"})
		return
	}
	c.JSON(http.StatusOK, s)
}

func (h *BillingHandler) AdminLedger(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, err := h.billing.ListLedger(c.Request.Context(), c.Query("account"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load ledger"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": rows})
}

func (h *BillingHandler) AdminMerchantBalance(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid merchant id"})
		return
	}
	bal, err := h.billing.MerchantBalance(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load balance"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"merchant_id": id, "balance": bal})
}

func (h *BillingHandler) AdminListPayouts(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.billing.ListPayouts(c.Request.Context(), nil, domain.PayoutStatus(c.Query("status")), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load payouts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"payouts": rows, "total": total})
}

func (h *BillingHandler) payoutError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPayoutNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Payout not found"})
	case errors.Is(err, service.ErrPayoutNotRequested):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update payout"})
	}
}

type markPaidBody struct {
	Reference string `json:"reference" binding:"required"`
}

func (h *BillingHandler) AdminMarkPayoutPaid(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payout id"})
		return
	}
	var req markPaidBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reference (transfer/transaction id) is required"})
		return
	}
	p, err := h.billing.MarkPayoutPaid(c.Request.Context(), id, c.MustGet("user_id").(uuid.UUID), req.Reference)
	if err != nil {
		h.payoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

type rejectPayoutBody struct {
	Note string `json:"note"`
}

func (h *BillingHandler) AdminRejectPayout(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payout id"})
		return
	}
	var req rejectPayoutBody
	_ = c.ShouldBindJSON(&req)
	p, err := h.billing.RejectPayout(c.Request.Context(), id, c.MustGet("user_id").(uuid.UUID), req.Note)
	if err != nil {
		h.payoutError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// ─── Admin: payments & refunds ───────────────────────────────────────────

func (h *BillingHandler) AdminListPayments(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, err := h.billing.ListPayments(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load payments"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"payments": rows})
}

func (h *BillingHandler) AdminListRefunds(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.billing.ListRefunds(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load refunds"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"refunds": rows, "total": total})
}

type refundBody struct {
	ID        uuid.UUID `json:"id" binding:"required"` // bkash transaction id (subscription) or order id (order)
	Reference string    `json:"reference" binding:"required"`
	Reason    string    `json:"reason"`
}

func (h *BillingHandler) refund(c *gin.Context, fn func(adminID, id uuid.UUID, ref, reason string) (*domain.Refund, error)) {
	var req refundBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id and reference (the bKash refund transaction id) are required"})
		return
	}
	r, err := fn(c.MustGet("user_id").(uuid.UUID), req.ID, req.Reference, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTransactionNotFound), errors.Is(err, service.ErrOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
		case errors.Is(err, service.ErrAlreadyRefunded):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrNotRefundable):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record refund"})
		}
		return
	}
	c.JSON(http.StatusCreated, r)
}

func (h *BillingHandler) AdminRefundSubscription(c *gin.Context) {
	h.refund(c, func(admin, id uuid.UUID, ref, reason string) (*domain.Refund, error) {
		return h.billing.RefundSubscriptionPayment(c.Request.Context(), admin, id, ref, reason)
	})
}

func (h *BillingHandler) AdminRefundOrder(c *gin.Context) {
	h.refund(c, func(admin, id uuid.UUID, ref, reason string) (*domain.Refund, error) {
		return h.billing.RefundOrder(c.Request.Context(), admin, id, ref, reason)
	})
}
