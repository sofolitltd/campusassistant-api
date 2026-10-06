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
		claims, err := Authenticate(c, jwtManager, db)
		if err != nil {
			c.AbortWithStatusJSON(err.Status, gin.H{"error": err.Message})
			return
		}

		SetUserContext(c, claims)
		c.Next()
	}
}

// AuthError carries the status the caller should return alongside the reason,
// so both JWTMiddleware and Guard can render the same failures consistently.
type AuthError struct {
	Status  int
	Message string
}

func (e *AuthError) Error() string { return e.Message }

// IsAdminRole reports whether a token's role is a dashboard admin. Roles live
// in two tables today (admins vs users), and this is the seam that the scoped
// role work will later replace with a permission lookup.
func IsAdminRole(role string) bool {
	return role == "super_admin" || role == "admin"
}

// Authenticate validates the request's token and confirms the account behind
// it is still live. It does not write to the context — see SetUserContext.
func Authenticate(c *gin.Context, jwtManager *auth.JWTManager, db *gorm.DB) (*auth.Claims, *AuthError) {
	// Authorization header, or — for WebSocket upgrades only, since browsers
	// and the Dart client can't set headers on them — a ?token= query param.
	// A token in an ordinary URL ends up in proxies, history and logs, so it
	// is not accepted anywhere else.
	tokenString := c.GetHeader("Authorization")
	if tokenString == "" && strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
		tokenString = c.Query("token")
	}
	if tokenString == "" {
		return nil, &AuthError{http.StatusUnauthorized, "Authorization header is required"}
	}

	// If from header, strip Bearer prefix
	if strings.HasPrefix(tokenString, "Bearer ") {
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")
	}

	// Validate token
	claims, err := jwtManager.ValidateToken(tokenString)
	if err != nil {
		if err == auth.ErrExpiredToken {
			return nil, &AuthError{http.StatusUnauthorized, "Token has expired"}
		}
		return nil, &AuthError{http.StatusUnauthorized, "Invalid token"}
	}

	var currentTokenVersion int
	if IsAdminRole(claims.Role) {
		var exists int
		err = db.Table("admins").Select("1").Where("id = ? AND is_active = true", claims.UserID).Scan(&exists).Error
		if err != nil || exists == 0 {
			return nil, &AuthError{http.StatusUnauthorized, "Admin not found"}
		}
		currentTokenVersion = 1
	} else {
		err = db.Table("users").Select("token_version").Where("id = ?", claims.UserID).Scan(&currentTokenVersion).Error
		if err != nil || currentTokenVersion == 0 {
			return nil, &AuthError{http.StatusUnauthorized, "User not found"}
		}
	}
	if currentTokenVersion != claims.TokenVersion {
		return nil, &AuthError{http.StatusUnauthorized, "Session expired, please login again"}
	}

	return claims, nil
}

// SetUserContext publishes the authenticated identity for downstream handlers.
func SetUserContext(c *gin.Context, claims *auth.Claims) {
	c.Set("user_id", claims.UserID)
	c.Set("user_email", claims.Email)
	c.Set("user_role", claims.Role)
	c.Set("university_id", claims.UniversityID)
	c.Set("department_id", claims.DepartmentID)
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
