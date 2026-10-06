package handler

import (
	"net/http"
	"strconv"

	"campusassistant-api/internal/domain"
	"campusassistant-api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// InsightsHandler serves seller analytics and product view counting.
type InsightsHandler struct {
	insights     *service.InsightsService
	merchantRepo domain.MerchantRepository
	db           *gorm.DB
}

func NewInsightsHandler(insights *service.InsightsService, merchantRepo domain.MerchantRepository, db *gorm.DB) *InsightsHandler {
	return &InsightsHandler{insights: insights, merchantRepo: merchantRepo, db: db}
}

// MerchantStats: GET /my/merchants/:id/stats?days=30
func (h *InsightsHandler) MerchantStats(c *gin.Context) {
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
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	stats, err := h.insights.Stats(c.Request.Context(), m.ID, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load stats"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// RecordView: POST /my/products/:id/view — counts one product-page open.
// Approximate on purpose (not de-duplicated per user); the app sends it once
// per visit.
func (h *InsightsHandler) RecordView(c *gin.Context) {
	id, ok := idParam(c, "id")
	if !ok {
		return
	}
	if err := h.db.WithContext(c.Request.Context()).
		Exec("UPDATE products SET view_count = view_count + 1 WHERE id = ? AND is_published = ?", id, true).Error; err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusNoContent)
}
