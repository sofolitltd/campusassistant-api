package handler

import (
	"net/http"
	"strconv"
	"time"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AuditLogHandler struct {
	db *gorm.DB
}

func NewAuditLogHandler(db *gorm.DB) *AuditLogHandler {
	return &AuditLogHandler{db: db}
}

func (h *AuditLogHandler) List(c *gin.Context) {
	query := h.db.WithContext(c.Request.Context()).Model(&domain.AuditLog{})
	for _, field := range []string{"actor_id", "entity_id", "university_id", "department_id", "batch_id"} {
		raw := c.Query(field)
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + field})
			return
		}
		query = query.Where(field+" = ?", id)
	}
	for field, column := range map[string]string{
		"actor_type": "actor_type", "action": "action", "entity_type": "entity_type",
	} {
		if value := c.Query(field); value != "" {
			query = query.Where(column+" = ?", value)
		}
	}
	for key, operator := range map[string]string{"from": ">=", "to": "<="} {
		raw := c.Query(key)
		if raw == "" {
			continue
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": key + " must be an RFC3339 timestamp"})
			return
		}
		query = query.Where("timestamp "+operator+" ?", value)
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		if value > 100 {
			value = 100
		}
		limit = value
	}
	offset := 0
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid offset"})
			return
		}
		offset = value
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query audit log"})
		return
	}
	var rows []domain.AuditLog
	if err := query.Order("timestamp DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query audit log"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "count": count, "limit": limit, "offset": offset})
}
