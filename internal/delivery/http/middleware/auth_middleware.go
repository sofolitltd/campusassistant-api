package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIKeyMiddleware validates the X-API-Key header against the configured key.
//
// This used to skip verification entirely when apiKey was empty, which meant a
// deploy that lost the env var silently served every route unauthenticated. It
// now fails closed; main.go refuses to boot without a key outside development,
// so reaching this branch at all means something is misconfigured.
//
// Note the key identifies a client, it does not authorize one. It is shipped
// to mobile app binaries and so must never be the only thing guarding a route —
// see middleware.Policy for the per-route credential.
func APIKeyMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Server is misconfigured: API key not set"})
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
