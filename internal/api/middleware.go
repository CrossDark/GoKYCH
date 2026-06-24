package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/apikey"
	"gokych/internal/auth/session"
	"gokych/internal/auth/user"
)

// key for stashing the current user in gin.Context.
const ctxUserKey = "currentUser"
const ctxSessionKey = "sessionMgr"

// registerSessionMgr stores the Manager on the engine for handlers to access.
func (s *Server) sessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxSessionKey, s.sessions)
		c.Next()
	}
}

// loadUserMiddleware resolves the current user (if any) and stashes it in the
// context under ctxUserKey. It also refreshes the session idle timer and
// persists the session after the handler runs.
func (s *Server) loadUserMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// X-API-Key takes precedence over the session cookie — a script
		// shouldn't have to manage a CSRF token / captcha just to call
		// /api/articles or read its own keys. Once a valid key is presented,
		// the corresponding user is loaded into the context exactly as if
		// they had logged in via the web UI.
		if key := c.GetHeader("X-API-Key"); key != "" {
			res, err := apikey.Verify(s.DB, key)
			if err != nil {
				slog.Error("api key verify", "err", err)
				// Fall through to session auth on DB error — better than
				// 500'ing every request just because the key table is
				// briefly unavailable.
			} else if res.OwnerID > 0 {
				u, err := user.GetByID(s.DB, res.OwnerID)
				if err == nil && u != nil {
					c.Set(ctxUserKey, u)
				}
			}
		}
		if _, set := c.Get(ctxUserKey); !set {
			u, err := s.sessions.CurrentUser(c.Request)
			if err == nil && u != nil {
				c.Set(ctxUserKey, u)
			}
		}
		c.Next()
		// Persist any session mutations (e.g. last_activity refresh). Surface
		// failures so "莫名掉登录" is debuggable instead of silently swallowed.
		if err := s.sessions.PersistSession(c.Writer, c.Request); err != nil {
			slog.Error("session persist", "err", err)
		}
	}
}

// CurrentUserFromContext returns the authenticated user, or nil.
func CurrentUserFromContext(c *gin.Context) *user.User {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return nil
	}
	u, _ := v.(*user.User)
	return u
}

// sessionMgrFromContext returns the session manager.
func sessionMgrFromContext(c *gin.Context) *session.Manager {
	v, _ := c.Get(ctxSessionKey)
	m, _ := v.(*session.Manager)
	return m
}

// csrfMiddleware checks the X-CSRF-Token header (or form field) against the
// session token for all mutating requests (POST/PUT/PATCH/DELETE).
func (s *Server) csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != http.MethodPost && method != http.MethodPut &&
			method != http.MethodPatch && method != http.MethodDelete {
			c.Next()
			return
		}
		token := c.GetHeader("X-CSRF-Token")
		if token == "" {
			token = c.PostForm("csrf_token")
		}
		if !s.sessions.VerifyCSRF(c.Request, token) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "安全验证失败，请刷新页面后重试。",
			})
			return
		}
		c.Next()
	}
}

// requireLogin aborts with 401 if the user is not authenticated.
func requireLogin(c *gin.Context) {
	if CurrentUserFromContext(c) == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "请先登录。",
		})
		return
	}
	c.Next()
}

// requireAdmin aborts with 401/403 if the user is not an admin/owner.
func requireAdmin(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	if !user.IsAdmin(u.Role) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "权限不足。"})
		return
	}
	c.Next()
}

// requireOwner aborts with 401/403 if the user is not the owner.
func requireOwner(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	if !user.IsOwner(u.Role) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "权限不足。"})
		return
	}
	c.Next()
}

// clientIP reports the request's IP using gin's c.ClientIP(), which honours
// the TrustedProxies configured in router.Setup. Untrusted forwarded headers
// are ignored, so a client can't spoof X-Forwarded-For to bypass rate
// limiting (the old hand-rolled XFF/X-Real-IP reader trusted them by default).
func clientIP(c *gin.Context) string {
	return c.ClientIP()
}
