package middleware

import (
	"log"
	"net/http"

	"campusassistant-api/pkg/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Policy is the credential a route demands.
//
// Every route must name one. Registration helpers take an Access value with no
// usable zero default, so adding a route without deciding its protection is a
// compile error rather than something to catch in review — which is how a
// large router ends up with unauthenticated CRUD on user records.
type Policy int

const (
	// Public — no credentials at all. Reserved for data the mobile app must
	// read before anyone can log in: signup walks university → department →
	// batch → session before a token exists.
	Public Policy = iota

	// AuthJWT — any logged-in user. Directories the app shows to signed-in
	// students but that must not be readable by the open internet.
	AuthJWT

	// AdminJWT — a dashboard admin. Everything that mutates shared structure,
	// plus anything holding personal data or credentials.
	AdminJWT
)

// Access pairs the policy for reads with the policy for writes. Most resources
// differ: a catalog can be world-readable while only an admin may change it.
type Access struct {
	Read  Policy
	Write Policy
}

// Guard builds the middleware for a policy.
//
// When enforce is false it logs what it *would* have rejected and lets the
// request through. That mode exists for the rollout: deploy, watch a day of
// real traffic for calls the classification got wrong, then switch on.
type Guard struct {
	jwt     *auth.JWTManager
	db      *gorm.DB
	enforce bool
}

func NewGuard(jwtManager *auth.JWTManager, db *gorm.DB, enforce bool) *Guard {
	if !enforce {
		log.Printf("[access] LOG-ONLY mode: auth failures will be logged, not blocked. Set ACCESS_ENFORCE=true to enforce.")
	}
	return &Guard{jwt: jwtManager, db: db, enforce: enforce}
}

// Require returns the middleware enforcing p.
func (g *Guard) Require(p Policy) gin.HandlerFunc {
	if p == Public {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		claims, authErr := Authenticate(c, g.jwt, g.db)

		var failure *AuthError
		switch {
		case authErr != nil:
			failure = authErr
		case p == AdminJWT && !IsAdminRole(claims.Role):
			failure = &AuthError{http.StatusForbidden, "Admin access required"}
		}

		if failure != nil {
			if !g.enforce {
				log.Printf("[access] would reject %s %s: %s", c.Request.Method, c.Request.URL.Path, failure.Message)
				c.Next()
				return
			}
			c.AbortWithStatusJSON(failure.Status, gin.H{"error": failure.Message})
			return
		}

		SetUserContext(c, claims)
		c.Next()
	}
}
