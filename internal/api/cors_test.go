package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCorsOriginAllowed is the happy path: a known origin gets the
// standard CORS response headers (and Vary: Origin so caches don't
// cross-contaminate responses between origins).
func TestCorsOriginAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"https://gokych.example.com"}))
	r.GET("/ping", func(c *gin.Context) { c.String(200, "pong") })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://gokych.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://gokych.example.com" {
		t.Errorf("Allow-Origin = %q, want exact origin (no wildcard with credentials)", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want \"true\"", got)
	}
	if got := w.Header().Get("Vary"); !contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include \"Origin\"", got)
	}
}

// TestCorsOriginNotAllowed: unknown origin gets NO CORS headers. The
// browser will refuse the response — that's the right behavior; we
// don't 4xx because the response status should come from the handler.
func TestCorsOriginNotAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"https://gokych.example.com"}))
	r.GET("/ping", func(c *gin.Context) { c.String(200, "pong") })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (handler should still run)", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty (origin not in allowlist)", got)
	}
}

// TestCorsPreflight: OPTIONS preflight short-circuits with 204 BEFORE
// reaching the actual handler. This is the critical guarantee — if
// preflights reached the CSRF middleware, every cross-origin mutation
// would 403 because the preflight doesn't carry a CSRF token.
func TestCorsPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlerCalled := false
	r := gin.New()
	r.Use(corsMiddleware([]string{"https://gokych.example.com"}))
	r.POST("/x", func(c *gin.Context) { handlerCalled = true; c.String(200, "ok") })

	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://gokych.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-CSRF-Token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if handlerCalled {
		t.Error("preflight reached the handler — must short-circuit at CORS middleware")
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); !contains(got, "POST") {
		t.Errorf("Allow-Methods = %q, must include POST", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); !contains(got, "X-CSRF-Token") {
		t.Errorf("Allow-Headers = %q, must include X-CSRF-Token", got)
	}
}

// TestCorsEmptyAllowList: with no allowed origins the middleware is a
// no-op. Same-origin requests still work (they don't send an Origin
// header); cross-origin requests get blocked by the browser naturally.
func TestCorsEmptyAllowList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware(nil))
	r.GET("/ping", func(c *gin.Context) { c.String(200, "pong") })

	// Same-origin — no Origin header, no CORS needed.
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("same-origin status = %d, want 200", w.Code)
	}

	// Cross-origin — middleware skips, no headers set, browser will refuse.
	req2 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req2.Header.Set("Origin", "https://anywhere.example")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("empty allowlist should set no headers; got %q", got)
	}
}

// TestPublicAssetURL: covers the prod-absolute / dev-relative branching
// for the helper that builds /uploads/* URLs in API responses.
func TestPublicAssetURL(t *testing.T) {
	s := &Server{} // PublicURL is the zero value
	if got := s.publicAssetURL("/uploads/abc.jpg"); got != "/uploads/abc.jpg" {
		t.Errorf("empty PublicURL: got %q, want relative path unchanged", got)
	}

	s.PublicURL = "https://api.example.com"
	if got := s.publicAssetURL("/uploads/abc.jpg"); got != "https://api.example.com/uploads/abc.jpg" {
		t.Errorf("with PublicURL: got %q", got)
	}

	// Trailing slash on PublicURL should NOT produce a double-slash
	// (some strict parsers / CDNs reject "https://x//uploads/...").
	s.PublicURL = "https://api.example.com/"
	if got := s.publicAssetURL("/uploads/abc.jpg"); got != "https://api.example.com/uploads/abc.jpg" {
		t.Errorf("trailing-slash PublicURL: got %q, want single-slash join", got)
	}
}

// contains is a tiny substring check used in this file's assertions
// (we can't pull strings just for these).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
