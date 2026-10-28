package middleware

import (
	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/telegram"
	"context"
	"fmt"
	"log"
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
//
// telegramClient may be nil (alerts disabled). Only genuine server errors
// (status >= 500) page admins on Telegram — 4xx is routine client misuse and
// would be noise at request volume.
//
// A natural access-token expiry or a token invalidated by logout-all/
// logout-others on another device is not a bug — it's the normal token
// lifecycle, and the client already silently refreshes and retries on it
// (see api_client.dart). Devices like /devices/heartbeat fire every 2
// minutes per active user, so without this filter these two cases alone
// dominate the error log and bury real signal (was ~9.5k/week on
// /api/v1/devices before this filter).
var benignAuthMessages = map[string]bool{
	"Token has expired":                   true,
	"Session expired, please login again": true,
}

func SystemLogger(db *gorm.DB, telegramClient *telegram.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method
		c.Next()

		status := c.Writer.Status()
		if status < 400 {
			return
		}

		authErr, _ := c.Get("auth_error")
		if authMsg, ok := authErr.(string); ok && status == 401 && benignAuthMessages[authMsg] {
			return
		}

		message := ""
		if authMsg, ok := authErr.(string); ok {
			message = authMsg
		} else if len(c.Errors) > 0 {
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

		if status >= 500 && telegramClient != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				text := fmt.Sprintf("Server error %d\n%s %s\n%s", status, method, path, message)
				if err := telegramClient.SendMessage(ctx, text); err != nil {
					log.Printf("[system_logger] telegram notify failed: %v", err)
				}
			}()
		}
	}
}
