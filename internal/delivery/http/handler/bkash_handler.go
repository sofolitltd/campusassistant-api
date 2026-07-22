package handler

import (
	"errors"
	"net/http"
	"strconv"

	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type BkashHandler struct {
	payments *service.PaymentService
}

func NewBkashHandler(payments *service.PaymentService) *BkashHandler {
	return &BkashHandler{payments: payments}
}

type createBkashPaymentRequest struct {
	PlanID uuid.UUID `json:"plan_id" binding:"required"`
}

// CreatePayment starts a bKash checkout session for the authenticated user.
// The amount charged is always derived server-side from the plan — the
// client only ever chooses which plan, never how much it costs.
func (h *BkashHandler) CreatePayment(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var req createBkashPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.payments.CreatePayment(c.Request.Context(), userID, req.PlanID)
	if err != nil {
		if errors.Is(err, service.ErrPlanNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Subscription plan not found"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to start bKash payment: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

type executeBkashPaymentRequest struct {
	PaymentID string `json:"payment_id" binding:"required"`
}

// ExecutePayment finalizes a payment. It re-verifies with bKash directly —
// a client cannot grant itself a subscription by simply claiming success.
func (h *BkashHandler) ExecutePayment(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	var req executeBkashPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sub, err := h.payments.ExecutePayment(c.Request.Context(), userID, req.PaymentID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTransactionNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
		case errors.Is(err, service.ErrForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": "This payment does not belong to you"})
		case errors.Is(err, service.ErrPaymentNotCompleted):
			c.JSON(http.StatusPaymentRequired, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrPlanNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Subscription plan not found"})
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to finalize bKash payment: " + err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, sub)
}

// ListTransactions returns the authenticated user's own bKash payment
// history (all statuses — completed, failed, cancelled, initiated).
func (h *BkashHandler) ListTransactions(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}

	txs, total, err := h.payments.ListTransactions(c.Request.Context(), userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load transaction history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transactions": txs, "total": total})
}
