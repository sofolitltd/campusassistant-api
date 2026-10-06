package middleware

import (
	"context"
	"net/http"

	"campusassistant-api/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// EntitlementChecker is satisfied by service.EntitlementService.
type EntitlementChecker interface {
	Check(ctx context.Context, userID uuid.UUID, feature string) (domain.FeatureAccess, error)
}

// RequireEntitlement blocks a route unless the user's plan includes feature
// and (for capped features) they have usage left. It must run after
// JWTMiddleware. It only checks; handlers that meter usage call
// EntitlementService.Consume themselves, once the work has succeeded.
//
//	r.GET("/premium/notes", middleware.JWTMiddleware(...), middleware.RequireEntitlement(ent, domain.FeaturePremiumContent), h.Notes)
func RequireEntitlement(svc EntitlementChecker, feature string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := c.Get("user_id")
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}
		access, err := svc.Check(c.Request.Context(), userID.(uuid.UUID), feature)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Could not verify your plan"})
			return
		}
		if !access.Allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "Your plan does not include this feature", "code": "entitlement_required",
				"feature": feature, "upgrade_required": true,
			})
			return
		}
		if access.Remaining == 0 {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "You have reached the limit for this feature", "code": "limit_reached",
				"feature": feature, "period": access.Period, "upgrade_required": true,
			})
			return
		}
		c.Next()
	}
}

// RequireAdmin allows only dashboard admins, always — unlike Guard it has no
// log-only rollout mode. Use it for routes where a misclassification means
// real money or entitlements (granting Pro, coupons, payouts). Must run after
// JWTMiddleware.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("user_role")
		if r, ok := role.(string); !ok || !IsAdminRole(r) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}
		c.Next()
	}
}
