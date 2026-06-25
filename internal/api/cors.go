package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// corsMiddleware handles CORS for the API. It's credentialed (the session
// cookie is sent on every cross-origin fetch from the admin UI), so
// wildcard "*" + credentials is forbidden by the spec — we MUST echo the
// exact request origin in the response header. The middleware compares the
// incoming Origin against an explicit whitelist and short-circuits OPTIONS
// preflights with 204 so they don't reach the CSRF / auth middleware
// (those would 403 the preflight and break the actual mutation request
// that follows it).
//
// Behavior:
//   - Empty allow list (or nil) → middleware is a no-op. The browser will
//     block any cross-origin fetch on its own, which is what we want for
//     dev when the frontend is on the same origin as the API.
//   - Origin in the list → echo it back, set Allow-Credentials, the
//     standard allow-methods/-headers, and Vary: Origin so caches don't
//     cross-contaminate responses between different allow-listed origins.
//   - Origin NOT in the list → don't set CORS headers. The browser will
//     reject the response, which is the right behavior; we don't 4xx
//     because that breaks the spec (the actual response from the handler
//     is what should carry the status).
func corsMiddleware(allowedOrigins []string) gin.HandlerFunc {
	// Build a set for O(1) lookup. nil/empty → set is empty, the
	// "is origin allowed" check below short-circuits to false.
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allow[o] = struct{}{}
	}

	// Static allow lists. Methods cover everything the API does; the
	// CSRF + X-API-Key custom headers are whitelisted so the browser
	// actually lets the real request through after the preflight.
	allowMethods := "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	allowHeaders := "Content-Type, X-CSRF-Token, X-API-Key"
	// 5 min: preflights are rare, and the spec lets us cache the result
	// so a refresh storm on the admin page doesn't double our OPTIONS
	// traffic. Bump to a day if you want; the value here is conservative.
	maxAge := "300"

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			// Not a CORS request (same-origin or non-browser client).
			// Skip — adding CORS headers here would be noise.
			c.Next()
			return
		}
		if _, ok := allow[origin]; !ok {
			// Origin not whitelisted. Don't leak info about the allow
			// list; just skip the headers and let the browser refuse the
			// response when it comes back.
			c.Next()
			return
		}

		// Origin is allowed — set the standard CORS response headers.
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Set("Access-Control-Allow-Methods", allowMethods)
		h.Set("Access-Control-Allow-Headers", allowHeaders)
		h.Set("Access-Control-Max-Age", maxAge)
		// Vary tells shared caches (CF, browser disk cache) that the
		// response varies by Origin — otherwise a CORS-allowed origin
		// could get served a response that was cached for a different
		// origin, leaking the wrong Access-Control-Allow-Origin back.
		h.Add("Vary", "Origin")

		// Preflight: handle here, never let it reach CSRF / auth / handler.
		// 204 is the conventional preflight success response; some clients
		// also accept 200, but 204 signals "no body, decision made" most
		// cleanly.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
