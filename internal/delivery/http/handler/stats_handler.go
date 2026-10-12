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

func (h *StatsHandler) GetUserStats(c *gin.Context) {
	stats, err := h.repo.GetUserStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user stats", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}
