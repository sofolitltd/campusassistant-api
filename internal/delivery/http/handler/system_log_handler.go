package handler

import (
	"net/http"
	"strconv"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SystemLogHandler serves the admin dashboard's Logs page — read-only, since
// rows are written exclusively by middleware.SystemLogger, never by a client.
type SystemLogHandler struct {
	db *gorm.DB
}

func NewSystemLogHandler(db *gorm.DB) *SystemLogHandler {
	return &SystemLogHandler{db: db}
}

// List godoc
// GET /system-logs?status_code=500&method=GET&path=students&from=...&to=...&limit=20&offset=0
func (h *SystemLogHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	query := h.db.Model(&domain.SystemLog{})

	if v := c.Query("status_code"); v != "" {
		if code, err := strconv.Atoi(v); err == nil {
			query = query.Where("status_code = ?", code)
		}
	}
	if v := c.Query("level"); v != "" {
		switch v {
		case "error":
			query = query.Where("status_code >= 500")
		case "warn":
			query = query.Where("status_code >= 400 AND status_code < 500")
		}
	}
	if v := c.Query("method"); v != "" {
		query = query.Where("method = ?", v)
	}
	if v := c.Query("path"); v != "" {
		query = query.Where("path ILIKE ?", "%"+v+"%")
	}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			query = query.Where("created_at <= ?", t)
		}
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var logs []domain.SystemLog
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": logs, "count": count})
}

// Stats godoc
// GET /system-logs/stats — counters for the dashboard's stat tiles.
func (h *SystemLogHandler) Stats(c *gin.Context) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := todayStart.AddDate(0, 0, -7)

	var todayCount, weekCount, serverErrorCount, clientErrorCount int64
	h.db.Model(&domain.SystemLog{}).Where("created_at >= ?", todayStart).Count(&todayCount)
	h.db.Model(&domain.SystemLog{}).Where("created_at >= ?", weekStart).Count(&weekCount)
	h.db.Model(&domain.SystemLog{}).Where("created_at >= ? AND status_code >= 500", weekStart).Count(&serverErrorCount)
	h.db.Model(&domain.SystemLog{}).Where("created_at >= ? AND status_code >= 400 AND status_code < 500", weekStart).Count(&clientErrorCount)

	type pathCount struct {
		Path  string `json:"path"`
		Count int64  `json:"count"`
	}
	var topPaths []pathCount
	h.db.Model(&domain.SystemLog{}).
		Select("path, count(*) as count").
		Where("created_at >= ?", weekStart).
		Group("path").
		Order("count DESC").
		Limit(5).
		Scan(&topPaths)

	c.JSON(http.StatusOK, gin.H{
		"today_total":        todayCount,
		"week_total":         weekCount,
		"week_server_errors": serverErrorCount,
		"week_client_errors": clientErrorCount,
		"top_failing_paths":  topPaths,
	})
}
