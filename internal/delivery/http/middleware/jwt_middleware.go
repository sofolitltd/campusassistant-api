package middleware

import (
	"campusassistant-api/pkg/auth"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// JWTMiddleware validates JWT tokens and sets user context
func JWTMiddleware(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			return
		}

		// Check if it's a Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format. Use: Bearer <token>"})
			return
		}

		tokenString := parts[1]

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
