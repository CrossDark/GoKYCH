package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/apikey"
)

const apiKeyTTL = 0 * time.Second

func (s *Server) listAPIKeys(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	ctx := c.Request.Context()
	keys, err := apikey.ListCtx(ctx, s.DB, u.ID)
	if err != nil {
		slog.Error("listAPIKeys", "user_id", u.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载 API Key 失败。"})
		return
	}
	if keys == nil {
		keys = []apikey.Key{}
	}
	c.JSON(http.StatusOK, keys)
}

type createAPIKeyInput struct {
	Name string `json:"name"`
	TTLDays int `json:"ttl_days"`
}

func (s *Server) createAPIKey(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	var in createAPIKeyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	ttl := apiKeyTTL
	if in.TTLDays > 0 {
		ttl = time.Duration(in.TTLDays) * 24 * time.Hour
	}
	ctx := c.Request.Context()
	key, plaintext, err := apikey.CreateCtx(ctx, s.DB, u.ID, in.Name, ttl)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	slog.Info("api key created", "user_id", u.ID, "key_id", key.ID, "name", key.Name)
	c.JSON(http.StatusCreated, gin.H{
		"id":            key.ID,
		"name":          key.Name,
		"key_prefix":    key.KeyPrefix,
		"expires_at":    key.ExpiresAt,
		"created_at":    key.CreatedAt,
		"plaintext_key": plaintext,
		"warning":       "请立即保存：此明文仅展示一次，再次查看需要重新创建。",
	})
}

func (s *Server) deleteAPIKey(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID。"})
		return
	}
	ctx := c.Request.Context()
	ok, err := apikey.DeleteCtx(ctx, s.DB, u.ID, id)
	if err != nil {
		slog.Error("deleteAPIKey", "user_id", u.ID, "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "API Key 不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
