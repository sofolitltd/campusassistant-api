package handler

import (
	"net/http"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type CouponHandler struct {
	repo domain.CouponRepository
}

func NewCouponHandler(repo domain.CouponRepository) *CouponHandler {
	return &CouponHandler{repo: repo}
}

type createCouponRequest struct {
	Code          string  `json:"code" binding:"required"`
	DiscountType  string  `json:"discount_type"`
	DiscountValue int     `json:"discount_value" binding:"required"`
	MaxUses       int     `json:"max_uses" binding:"required,min=1"`
	PlanID        *string `json:"plan_id"`
	MinAmount     int     `json:"min_amount"`
	ExpiresAt     *string `json:"expires_at"`
}

func (h *CouponHandler) Create(c *gin.Context) {
	var req createCouponRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	coupon := &domain.CouponCode{
		Code:          req.Code,
		DiscountType:  domain.CouponDiscountType(req.DiscountType),
		DiscountValue: req.DiscountValue,
		MaxUses:       req.MaxUses,
		MinAmount:     req.MinAmount,
	}
	if coupon.DiscountType == "" {
		coupon.DiscountType = domain.CouponPercentage
	}
	if req.PlanID != nil {
		pid, err := uuid.Parse(*req.PlanID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan_id"})
			return
		}
		coupon.PlanID = &pid
	}
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expires_at, expected RFC3339 format"})
			return
		}
		coupon.ExpiresAt = &t
	}

	if err := h.repo.Create(c.Request.Context(), coupon); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create coupon: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, coupon)
}

func (h *CouponHandler) GetAll(c *gin.Context) {
	coupons, err := h.repo.GetAll(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, coupons)
}

type updateCouponRequest struct {
	DiscountType  *string `json:"discount_type"`
	DiscountValue *int    `json:"discount_value"`
	MaxUses       *int    `json:"max_uses"`
	PlanID        *string `json:"plan_id"`
	MinAmount     *int    `json:"min_amount"`
	ExpiresAt     *string `json:"expires_at"`
	IsActive      *bool   `json:"is_active"`
}

func (h *CouponHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateCouponRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	coupon, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Coupon not found"})
		return
	}

	if req.DiscountType != nil {
		coupon.DiscountType = domain.CouponDiscountType(*req.DiscountType)
	}
	if req.DiscountValue != nil {
		coupon.DiscountValue = *req.DiscountValue
	}
	if req.MaxUses != nil {
		coupon.MaxUses = *req.MaxUses
	}
	if req.PlanID != nil {
		pid, err := uuid.Parse(*req.PlanID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan_id"})
			return
		}
		coupon.PlanID = &pid
	}
	if req.MinAmount != nil {
		coupon.MinAmount = *req.MinAmount
	}
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expires_at, expected RFC3339 format"})
			return
		}
		coupon.ExpiresAt = &t
	}
	if req.IsActive != nil {
		coupon.IsActive = *req.IsActive
	}

	if err := h.repo.Update(c.Request.Context(), coupon); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update coupon: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, coupon)
}
