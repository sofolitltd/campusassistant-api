package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RateLimiter is an in-memory token bucket per key. State is per process: with
// N instances behind a load balancer the effective limit is up to N× — fine
// for abuse/brute-force protection, not for exact quotas (use a shared store
// such as Redis if that is ever needed).
type RateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // tokens per second
	burst     float64
	now       func() time.Time
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter allows `limit` requests per `per`, with bursts up to `limit`.
func NewRateLimiter(limit int, per time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: map[string]*bucket{},
		rate:    float64(limit) / per.Seconds(),
		burst:   float64(limit),
		now:     time.Now,
	}
}

// Allow takes one token for key. When it refuses, retryAfter says how long
// until a token is available.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep drops buckets that have been idle long enough to be full again, so
// the map can't grow without bound under a flood of distinct keys. Caller
// holds mu.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	idle := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) >= idle {
			delete(l.buckets, k)
		}
	}
}

// ByIP keys requests by client IP.
func ByIP(c *gin.Context) string { return "ip:" + c.ClientIP() }

// ByUserOrIP keys by the authenticated user when there is one (so a whole
// campus behind one NAT isn't throttled as a single client), else by IP.
// Mount it after JWTMiddleware to get the per-user behaviour.
func ByUserOrIP(c *gin.Context) string {
	if id, ok := c.Get("user_id"); ok {
		if uid, ok := id.(uuid.UUID); ok {
			return "user:" + uid.String()
		}
	}
	return ByIP(c)
}

// RateLimit rejects requests over the limit with 429 and a Retry-After header.
func RateLimit(l *RateLimiter, key func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok, retry := l.Allow(key(c)); !ok {
			secs := int(math.Ceil(retry.Seconds()))
			if secs < 1 {
				secs = 1
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many requests, please slow down", "retry_after_seconds": secs,
			})
			return
		}
		c.Next()
	}
}

// NoopMiddleware is used when a feature (e.g. rate limiting) is switched off.
func NoopMiddleware() gin.HandlerFunc { return func(c *gin.Context) { c.Next() } }
