package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
)

// GET /api/articles/{type}/{slug}/rating
func (s *Server) getRating(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	userName := ""
	if u := CurrentUserFromContext(c); u != nil {
		userName = u.Username
	}
	rs, err := content.GetRatingSummary(s.DB, articleID, userName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评分失败。"})
		return
	}
	c.JSON(http.StatusOK, rs)
}

// POST /api/articles/{type}/{slug}/rating
func (s *Server) setRating(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	var in struct {
		Score float64 `json:"score"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误，需要 score 字段。"})
		return
	}
	if in.Score < -1 || in.Score > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "评分范围需在 -1 到 1 之间。"})
		return
	}
	userName := "匿名"
	if u := CurrentUserFromContext(c); u != nil {
		userName = u.Username
	}
	rs, err := content.SetRating(s.DB, articleID, userName, in.Score)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "评分失败。"})
		return
	}
	c.JSON(http.StatusOK, rs)
}

// DELETE /api/articles/{type}/{slug}/rating
func (s *Server) deleteRating(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	userName := ""
	if u := CurrentUserFromContext(c); u != nil {
		userName = u.Username
	}
	if userName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要登录。"})
		return
	}
	ok, err := content.DeleteRating(s.DB, articleID, userName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "取消评分失败。"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到评分记录。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
