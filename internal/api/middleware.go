package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/apikey"
	"gokych/internal/auth/session"
	"gokych/internal/auth/user"
)

const ctxUserKey = "currentUser"
const ctxSessionKey = "sessionMgr"
const ctxAuthMethodKey = "authMethod"

func (s *Server) sessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxSessionKey, s.sessions)
		c.Next()
	}
}

func (s *Server) loadUserMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if key := c.GetHeader("X-API-Key"); key != "" {
			res, err := apikey.VerifyCtx(ctx, s.DB, key)
			if err != nil {
				slog.Error("api key verify", "err", err)
			} else if res.OwnerID > 0 {
				u, err := user.GetByIDCtx(ctx, s.DB, res.OwnerID)
				if err == nil && u != nil {
					c.Set(ctxUserKey, u)
					c.Set(ctxAuthMethodKey, "apikey")
				}
			}
		}
		if _, set := c.Get(ctxUserKey); !set {
			u, err := s.sessions.CurrentUserCtx(ctx, c.Request)
			if err == nil && u != nil {
				c.Set(ctxUserKey, u)
			}
		}
		c.Next()
		if err := s.sessions.PersistSession(c.Writer, c.Request); err != nil {
			slog.Error("session persist", "err", err)
		}
	}
}

func CurrentUserFromContext(c *gin.Context) *user.User {
	v, ok := c.Get(ctxUserKey)
	if !ok {
		return nil
	}
	u, _ := v.(*user.User)
	return u
}

func sessionMgrFromContext(c *gin.Context) *session.Manager {
	v, _ := c.Get(ctxSessionKey)
	m, _ := v.(*session.Manager)
	return m
}

func (s *Server) csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != http.MethodPost && method != http.MethodPut &&
			method != http.MethodPatch && method != http.MethodDelete {
			c.Next()
			return
		}
		if v, ok := c.Get(ctxAuthMethodKey); ok && v == "apikey" {
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

func requireLogin(c *gin.Context) {
	if CurrentUserFromContext(c) == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "请先登录。",
		})
		return
	}
	c.Next()
}

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

func clientIP(c *gin.Context) string {
	return c.ClientIP()
}
