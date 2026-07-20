package middleware

import (
	"campusassistant-api/pkg/auth"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// JWTMiddleware validates JWT tokens and sets user context. It also checks
// the token's embedded TokenVersion against the user's current value in the
// DB, so a logout-all/logout-others action can invalidate already-issued
// access tokens instantly instead of waiting for them to naturally expire.
//
// Performance note: this adds one PK lookup per authenticated request. If
// this becomes a bottleneck under load, cache token_version (e.g. in Redis)
// with a short TTL instead of hitting Postgres every time.
func JWTMiddleware(jwtManager *auth.JWTManager, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get token from Authorization header or query param (for WebSocket)
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			tokenString = c.Query("token")
		}
		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header or token query param is required"})
			return
		}

		// If from header, strip Bearer prefix
		if strings.HasPrefix(tokenString, "Bearer ") {
			tokenString = strings.TrimPrefix(tokenString, "Bearer ")
		}

		// Validate token
		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			if err == auth.ErrExpiredToken {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token has expired"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			return
		}

		var currentTokenVersion int
		if err := db.Table("users").Select("token_version").Where("id = ?", claims.UserID).Scan(&currentTokenVersion).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
			return
		}
		if currentTokenVersion != claims.TokenVersion {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Session expired, please login again"})
			return
		}

		// Set user information in context
		c.Set("user_id", claims.UserID)
		c.Set("user_email", claims.Email)
		c.Set("user_role", claims.Role)
		c.Set("university_id", claims.UniversityID)
		c.Set("department_id", claims.DepartmentID)

		c.Next()
	}
}

// RoleMiddleware checks if the user has one of the required roles
func RoleMiddleware(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("user_role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "User role not found in context"})
			return
		}

		role := userRole.(string)

		// Check if user's role is in the allowed roles
		for _, allowedRole := range allowedRoles {
			if role == allowedRole {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
	}
}

// UniversityMiddleware ensures the user belongs to a specific university
func UniversityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		universityID, exists := c.Get("university_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "University context required"})
			return
		}

		// Validate it's not a nil UUID
		if universityID.(uuid.UUID) == uuid.Nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "User must belong to a university"})
			return
		}

		c.Next()
	}
}

// DepartmentMiddleware ensures the user belongs to a specific department
func DepartmentMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		departmentID, exists := c.Get("department_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Department context required"})
			return
		}

		// Validate it's not a nil UUID
		if departmentID.(uuid.UUID) == uuid.Nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "User must belong to a department"})
			return
		}

		c.Next()
	}
}
