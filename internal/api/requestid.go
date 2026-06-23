package api

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const ctxRequestIDKey = "requestID"

// requestIDMiddleware stamps each request with a unique id (UUID v4), exposes
// it to callers via the gin context and the X-Request-ID response header, and
// emits a single structured access log line via slog. It also records basic
// metrics (request count, status distribution, average latency) on the Server's
// Metrics collector.
func (s *Server) requestIDMiddleware() gin.HandlerFunc {
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
		status := c.Writer.Status()

		slog.Info("request",
			"rid", id,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"ip", c.ClientIP(),
		)

		if s.Metrics != nil {
			s.Metrics.Record(status, latency)
		}
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
