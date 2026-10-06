package handler

import (
	"net/http"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MarketplaceAdminHandler groups the operator-facing marketplace controls:
// the commission promo, featured listings, seller stats and health metrics,
// plus a seller's view of their own fee.
type MarketplaceAdminHandler struct {
	db           *gorm.DB
	commission   *service.CommissionService
	insights     *service.InsightsService
	merchantRepo domain.MerchantRepository
}

func NewMarketplaceAdminHandler(db *gorm.DB, c *service.CommissionService, i *service.InsightsService, m domain.MerchantRepository) *MarketplaceAdminHandler {
	return &MarketplaceAdminHandler{db: db, commission: c, insights: i, merchantRepo: m}
}

type policyRequest struct {
	PromoActive bool    `json:"promo_active"`
	PromoRate   float64 `json:"promo_rate"`
	PromoDays   int     `json:"promo_days"`
}

// GetPolicy: GET /billing/commission-policy
func (h *MarketplaceAdminHandler) GetPolicy(c *gin.Context) {
	p, err := h.commission.Policy(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load policy"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// SetPolicy: PUT /billing/commission-policy
func (h *MarketplaceAdminHandler) SetPolicy(c *gin.Context) {
	var req policyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.commission.SetPolicy(c.Request.Context(), req.PromoActive, req.PromoRate, req.PromoDays)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Metrics: GET /billing/marketplace-metrics
func (h *MarketplaceAdminHandler) Metrics(c *gin.Context) {
	m, err := h.insights.Metrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load metrics"})
		return
	}
	c.JSON(http.StatusOK, m)
}

// MerchantStats: GET /merchants/:id/stats — the same numbers a seller sees.
func (h *MarketplaceAdminHandler) MerchantStats(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	stats, err := h.insights.Stats(c.Request.Context(), id, 30)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load stats"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

type featureRequest struct {
	// Days to feature the product from now; 0 removes the feature.
	Days int `json:"days"`
}

// FeatureProduct: PUT /products/:id/feature
func (h *MarketplaceAdminHandler) FeatureProduct(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	var req featureRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Days < 0 || req.Days > 365 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be between 0 and 365"})
		return
	}
	var until *time.Time
	if req.Days > 0 {
		t := time.Now().AddDate(0, 0, req.Days)
		until = &t
	}
	// featured_until is read-only through GORM, so write it directly.
	res := h.db.WithContext(c.Request.Context()).Exec("UPDATE products SET featured_until = ? WHERE id = ?", until, id)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update product"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"featured_until": until})
}

// MyCommission: GET /my/merchants/:id/commission — a seller's current fee.
func (h *MarketplaceAdminHandler) MyCommission(c *gin.Context) {
	userID := c.MustGet("user_id").(uuid.UUID)
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	m, err := h.merchantRepo.GetMerchantByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant not found"})
		return
	}
	if m.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not your merchant"})
		return
	}
	info, err := h.commission.Describe(c.Request.Context(), m, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load commission"})
		return
	}
	c.JSON(http.StatusOK, info)
}

// PublicPromo: GET /marketplace/promo — whether a new-seller promo is running,
// so the app can invite people to sell. No merchant data.
func (h *MarketplaceAdminHandler) PublicPromo(c *gin.Context) {
	p, err := h.commission.Policy(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"active": p.PromoActive, "rate": p.PromoRate, "days": p.PromoDays})
}
