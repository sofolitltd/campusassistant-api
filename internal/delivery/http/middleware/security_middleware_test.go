package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campusassistant-api/internal/domain"
	"campusassistant-api/pkg/auth"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() { gin.SetMode(gin.TestMode) }

func do(r http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ─── CORS ────────────────────────────────────────────────────────────────

func corsRouter(cfg CORSConfig) *gin.Engine {
	r := gin.New()
	r.Use(CORSMiddleware(cfg))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })
	return r
}

func TestCORS_OnlyAllowListedOriginsGetHeaders(t *testing.T) {
	r := corsRouter(CORSConfig{AllowedOrigins: []string{"https://app.example.com/some/path"}})

	w := do(r, "GET", "/x", map[string]string{"Origin": "https://app.example.com"})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allowed origin: ACAO = %q", got)
	}

	w = do(r, "GET", "/x", map[string]string{"Origin": "https://evil.example"})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("evil origin got ACAO %q, want none", got)
	}

	// No Origin = a mobile app or server: untouched, no CORS headers needed.
	w = do(r, "GET", "/x", nil)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("no-origin request: code=%d ACAO=%q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_PreflightFromUnknownOriginIsRejected(t *testing.T) {
	r := corsRouter(CORSConfig{AllowedOrigins: []string{"https://app.example.com"}})

	w := do(r, "OPTIONS", "/x", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("evil preflight = %d, want 403", w.Code)
	}
	w = do(r, "OPTIONS", "/x", map[string]string{"Origin": "https://app.example.com", "Access-Control-Request-Method": "GET"})
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("good preflight = %d, methods=%q", w.Code, w.Header().Get("Access-Control-Allow-Methods"))
	}
}

func TestCORS_LocalhostOnlyWhenEnabledAndWildcardIsOptIn(t *testing.T) {
	prod := corsRouter(CORSConfig{AllowedOrigins: []string{"https://app.example.com"}})
	if do(prod, "GET", "/x", map[string]string{"Origin": "http://localhost:3000"}).Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("localhost allowed in production config")
	}
	dev := corsRouter(CORSConfig{AllowLocalhost: true})
	if do(dev, "GET", "/x", map[string]string{"Origin": "http://localhost:3000"}).Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("localhost not allowed in dev config")
	}
	if do(dev, "GET", "/x", map[string]string{"Origin": "http://localhost.evil.com"}).Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("localhost.evil.com must not match the localhost rule")
	}
	wild := corsRouter(CORSConfig{AllowedOrigins: []string{"*"}})
	if do(wild, "GET", "/x", map[string]string{"Origin": "https://anything.example"}).Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("explicit * should allow everything")
	}
}

// ─── rate limiting ───────────────────────────────────────────────────────

func TestRateLimiter_AllowsBurstThenRefills(t *testing.T) {
	l := NewRateLimiter(3, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d refused inside burst", i)
		}
	}
	ok, retry := l.Allow("k")
	if ok || retry <= 0 || retry > 21*time.Second {
		t.Fatalf("4th: ok=%v retry=%v, want refused with ~20s retry", ok, retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("a different key must have its own bucket")
	}
	now = now.Add(21 * time.Second) // 3/min = one token every 20s
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("token should have refilled")
	}
}

func TestRateLimiter_SweepsIdleBuckets(t *testing.T) {
	l := NewRateLimiter(10, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		l.Allow(uuid.NewString())
	}
	now = now.Add(2 * time.Minute)
	l.Allow("trigger")
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("buckets = %d, want 1 after sweep (memory must not grow unbounded)", n)
	}
}

func TestRateLimit_Returns429WithRetryAfter(t *testing.T) {
	r := gin.New()
	r.Use(RateLimit(NewRateLimiter(2, time.Minute), ByIP))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })

	for i := 0; i < 2; i++ {
		if w := do(r, "GET", "/x", nil); w.Code != 200 {
			t.Fatalf("request %d = %d", i, w.Code)
		}
	}
	w := do(r, "GET", "/x", nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over limit: code=%d Retry-After=%q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestByUserOrIP_SeparatesUsersBehindOneAddress(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) { // stand-in for JWTMiddleware
		if id := c.GetHeader("X-Test-User"); id != "" {
			c.Set("user_id", uuid.MustParse(id))
		}
	})
	r.Use(RateLimit(NewRateLimiter(1, time.Minute), ByUserOrIP))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })
	a, b := uuid.NewString(), uuid.NewString()

	if do(r, "GET", "/x", map[string]string{"X-Test-User": a}).Code != 200 || do(r, "GET", "/x", map[string]string{"X-Test-User": b}).Code != 200 {
		t.Fatal("two users on the same IP should each get their own allowance")
	}
	if do(r, "GET", "/x", map[string]string{"X-Test-User": a}).Code != http.StatusTooManyRequests {
		t.Fatal("same user should be limited")
	}
}

// ─── access log never contains credentials ───────────────────────────────

func TestAccessLogger_OmitsQueryString(t *testing.T) {
	var buf bytes.Buffer
	r := gin.New()
	r.Use(accessLogger(&buf))
	r.GET("/ws/chat/1", func(c *gin.Context) { c.Status(200) })
	do(r, "GET", "/ws/chat/1?token=SECRET.JWT.VALUE", nil)

	out := buf.String()
	if strings.Contains(out, "SECRET") || strings.Contains(out, "token=") {
		t.Fatalf("log leaks the token: %q", out)
	}
	if !strings.Contains(out, "/ws/chat/1") {
		t.Fatalf("log missing path: %q", out)
	}
}

// ─── JWT: query-string tokens only on WebSocket upgrades ─────────────────

func TestAuthenticate_QueryTokenOnlyAcceptedForWebSocketUpgrade(t *testing.T) {
	jwt := auth.NewJWTManager("test-secret-test-secret-test-secret", time.Minute, time.Hour)
	r := gin.New()
	r.GET("/x", JWTMiddleware(jwt, nil), func(c *gin.Context) { c.String(200, "ok") })

	// A garbage token in the URL of a normal request is not even looked at.
	w := do(r, "GET", "/x?token=abc", nil)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "Authorization header is required") {
		t.Fatalf("plain request with ?token: code=%d body=%s", w.Code, w.Body.String())
	}
	// On a WebSocket upgrade it is read (and rejected here as invalid).
	w = do(r, "GET", "/x?token=abc", map[string]string{"Upgrade": "websocket"})
	if w.Code != 401 || !strings.Contains(w.Body.String(), "Invalid token") {
		t.Fatalf("websocket request with ?token: code=%d body=%s", w.Code, w.Body.String())
	}
}

// ─── RequireAdmin / RequireEntitlement ───────────────────────────────────

func asRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if role != "" {
			c.Set("user_role", role)
			c.Set("user_id", uuid.New())
		}
	}
}

func TestRequireAdmin(t *testing.T) {
	cases := map[string]int{"": 403, "student": 403, "teacher": 403, "admin": 200, "super_admin": 200}
	for role, want := range cases {
		r := gin.New()
		r.POST("/grant", asRole(role), RequireAdmin(), func(c *gin.Context) { c.String(200, "granted") })
		if got := do(r, "POST", "/grant", nil).Code; got != want {
			t.Errorf("role %q = %d, want %d", role, got, want)
		}
	}
}

type fakeChecker struct{ access domain.FeatureAccess }

func (f fakeChecker) Check(context.Context, uuid.UUID, string) (domain.FeatureAccess, error) {
	return f.access, nil
}

func TestRequireEntitlement(t *testing.T) {
	cases := []struct {
		name   string
		access domain.FeatureAccess
		want   int
	}{
		{"not entitled", domain.FeatureAccess{}, 403},
		{"entitled unlimited", domain.FeatureAccess{Allowed: true, Limit: -1, Remaining: -1}, 200},
		{"entitled with quota left", domain.FeatureAccess{Allowed: true, Limit: 5, Remaining: 2}, 200},
		{"quota exhausted", domain.FeatureAccess{Allowed: true, Limit: 5, Remaining: 0}, 429},
	}
	for _, tc := range cases {
		r := gin.New()
		r.GET("/p", asRole("student"), RequireEntitlement(fakeChecker{tc.access}, "premium_content"), func(c *gin.Context) { c.String(200, "ok") })
		if got := do(r, "GET", "/p", nil).Code; got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}
	// Unauthenticated (no user in context) is a 401, not a pass.
	r := gin.New()
	r.GET("/p", RequireEntitlement(fakeChecker{}, "x"), func(c *gin.Context) { c.String(200, "ok") })
	if got := do(r, "GET", "/p", nil).Code; got != 401 {
		t.Errorf("no user = %d, want 401", got)
	}
}
