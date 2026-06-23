package api

import (
	"github.com/gin-gonic/gin"
)

// securityHeaders injects a baseline set of browser security response headers
// on every response. They're defensive defaults; individual handlers can still
// override a header when they genuinely need to (e.g. CSP for a specific page).
//
//   - X-Content-Type-Options: nosniff        — stop MIME sniffing
//   - X-Frame-Options: DENY                  — anti-clickjacking (site doesn't frame itself)
//   - Referrer-Policy: strict-origin-when-cross-origin — leak less referrer to 3rd parties
//   - X-XSS-Protection: 0                    — Auditor is deprecated & buggy; rely on CSP instead
//   - Content-Security-Policy: default-src 'self' — baseline allowlist (page can tighten/loosen)
//
// HSTS is intentionally NOT set globally: it only makes sense behind HTTPS and
// should be added by the TLS-terminating reverse proxy in production.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-XSS-Protection", "0")
		// Only stamp CSP if the handler hasn't set a tighter one already.
		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		}
		c.Next()
	}
}
