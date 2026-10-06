package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

const corsAllowHeaders = "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-API-Key"

// CORSConfig controls which browser origins may call the API.
type CORSConfig struct {
	// AllowedOrigins are exact origins, e.g. "https://campusassistant.web.app".
	// A single "*" opts back into allowing every origin.
	AllowedOrigins []string
	// AllowLocalhost additionally allows http://localhost:* and
	// http://127.0.0.1:* — for local development only.
	AllowLocalhost bool
}

// NormalizeOrigin reduces a URL or origin to "scheme://host[:port]" (the form
// browsers send in the Origin header), or "" if it isn't a valid origin.
func NormalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// CORSMiddleware replaces the old blanket "Access-Control-Allow-Origin: *".
//
// Mobile apps and server-to-server callers send no Origin header and are
// unaffected. Browsers get CORS headers only for allow-listed origins, so an
// arbitrary website can no longer script the API from a visitor's browser.
// (Auth is a Bearer token, not a cookie, so this is defence in depth rather
// than the primary control — but the wildcard was never necessary.)
func CORSMiddleware(cfg CORSConfig) gin.HandlerFunc {
	allowed := map[string]bool{}
	allowAll := false
	for _, o := range cfg.AllowedOrigins {
		n := NormalizeOrigin(o)
		switch n {
		case "":
		case "*":
			allowAll = true
		default:
			allowed[n] = true
		}
	}

	isAllowed := func(origin string) bool {
		if allowAll || allowed[strings.ToLower(origin)] {
			return true
		}
		if cfg.AllowLocalhost {
			u, err := url.Parse(origin)
			return err == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
		}
		return false
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		preflight := c.Request.Method == http.MethodOptions

		if origin != "" {
			h := c.Writer.Header()
			h.Add("Vary", "Origin")
			if isAllowed(origin) {
				if allowAll {
					h.Set("Access-Control-Allow-Origin", "*")
				} else {
					h.Set("Access-Control-Allow-Origin", origin)
				}
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")
				h.Set("Access-Control-Max-Age", "600")
			} else if preflight {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}

		if preflight {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
