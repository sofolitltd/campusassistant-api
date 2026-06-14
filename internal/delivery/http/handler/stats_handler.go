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
