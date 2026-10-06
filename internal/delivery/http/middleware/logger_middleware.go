package middleware

import (
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"
)

// AccessLogger logs one line per request — method, path, status, latency, IP
// — but never the query string. Gin's default logger records the raw URL,
// which for WebSocket connections includes ?token=<JWT>, putting live
// credentials into server logs.
func AccessLogger() gin.HandlerFunc { return accessLogger(gin.DefaultWriter) }

func accessLogger(w io.Writer) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path // deliberately not RequestURI/RawQuery
		c.Next()
		fmt.Fprintf(w, "[GIN] %s | %3d | %13v | %15s | %-7s %s\n",
			start.Format("2006/01/02 - 15:04:05"), c.Writer.Status(), time.Since(start).Truncate(time.Microsecond),
			c.ClientIP(), c.Request.Method, path)
	}
}
