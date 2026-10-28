package handler

import (
	"campusassistant-api/internal/domain"
	"net/http"

	"github.com/gin-gonic/gin"
)

type StatsHandler struct {
	repo domain.StatsRepository
}

func NewStatsHandler(repo domain.StatsRepository) *StatsHandler {
	return &StatsHandler{repo: repo}
}

func (h *StatsHandler) GetDashboardStats(c *gin.Context) {
	stats, err := h.repo.GetDashboardStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch stats", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetUserGrowth serves the dashboard chart: ?range=today|7d|30d|12m|lifetime.
func (h *StatsHandler) GetUserGrowth(c *gin.Context) {
	growth, err := h.repo.GetUserGrowth(c.Request.Context(), c.DefaultQuery("range", "7d"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user growth", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, growth)
}

// GetSubscriptionAnalytics serves the Subscriptions page's Analytics tab:
// ?range=today|7d|30d|12m|lifetime.
func (h *StatsHandler) GetSubscriptionAnalytics(c *gin.Context) {
	analytics, err := h.repo.GetSubscriptionAnalytics(c.Request.Context(), c.DefaultQuery("range", "30d"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch subscription analytics", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, analytics)
}

func (h *StatsHandler) GetUserStats(c *gin.Context) {
	stats, err := h.repo.GetUserStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user stats", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// GetActiveUsers serves the dashboard's real-time active-user tile —
// distinct users seen (via device heartbeat) in the last 5 minutes, today,
// 7 days and 30 days.
func (h *StatsHandler) GetActiveUsers(c *gin.Context) {
	stats, err := h.repo.GetActiveUserStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch active user stats", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}
