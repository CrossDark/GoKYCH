package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const ctxRequestIDKey = "requestID"

// requestIDMiddleware stamps each request with a unique id (UUID v4), exposes
// it to callers via the gin context and the X-Request-ID response header, and
// emits a single structured access line. Replaces gin.Logger so the access log
// carries the request id for correlation with per-handler log.Printf lines.
//
// This is the minimal observability layer from P3-26; a full slog migration of
// the existing [module] log.Printf calls is left as a follow-up so this batch
// stays reviewable.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(ctxRequestIDKey, id)
		c.Writer.Header().Set("X-Request-ID", id)

		start := time.Now()
		c.Next()

		latency := time.Since(start)
		gin.DefaultErrorWriter.Write([]byte(
			"[access] rid=" + id +
				" method=" + c.Request.Method +
				" path=" + c.Request.URL.Path +
				" status=" + itoa(c.Writer.Status()) +
				" latency=" + latency.String() +
				" ip=" + c.ClientIP() + "\n",
		))
	}
}

// RequestIDFromContext returns the request id stashed on the context, or "".
// Handlers can use it to prefix their own log lines for correlation.
func RequestIDFromContext(c *gin.Context) string {
	v, ok := c.Get(ctxRequestIDKey)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// itoa is a dependency-free int→string for the access log (avoids pulling
// strconv into a hot middleware path unnecessarily; fmt would alloc more).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
