package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIKeyMiddleware validates the X-API-Key header against the configured key.
func APIKeyMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// If API key is not configured, skip verification (useful for dev)
		if apiKey == "" {
			c.Next()
			return
		}

		key := c.GetHeader("X-API-Key")
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key is required"})
			return
		}

		if key != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}

		c.Next()
	}
}
