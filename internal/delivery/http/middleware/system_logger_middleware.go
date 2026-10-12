package middleware

import (
	"campusassistant-api/internal/domain"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SystemLogger persists one row per failed (status >= 400) request so the
// admin dashboard's Logs page has something queryable to show, instead of
// only the stdout line AccessLogger prints. It never logs query strings,
// same reasoning as AccessLogger (WebSocket ?token=<JWT> must never land in
// a log a browser can read back). The write happens in a goroutine so a
// logging hiccup never adds latency to the response the client is waiting on.
func SystemLogger(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method
		c.Next()

		status := c.Writer.Status()
		if status < 400 {
			return
		}

		message := ""
		if len(c.Errors) > 0 {
			message = c.Errors.String()
		}

		var userID *uuid.UUID
		if v, ok := c.Get("user_id"); ok {
			if id, ok := v.(uuid.UUID); ok {
				userID = &id
			}
		}

		entry := domain.SystemLog{
			Method:     method,
			Path:       path,
			StatusCode: status,
			Message:    message,
			UserID:     userID,
			IPAddress:  c.ClientIP(),
			LatencyMs:  time.Since(start).Milliseconds(),
		}

		go func() {
			db.Create(&entry)
		}()
	}
}
