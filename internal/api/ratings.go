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
	voterKey := ""
	if u := CurrentUserFromContext(c); u != nil {
		voterKey = content.VoterKey(&u.ID, u.Username)
	}
	rs, err := content.GetRatingSummary(s.DB, articleID, voterKey)
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
	// Logged-in raters are keyed on their session user id (authorName is just
	// a display name set from the session, so it can't be spoofed). Anonymous
	// raters share the "匿名" display name and the "n:匿名" voter key.
	var userID *int
	authorName := "匿名"
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		userID = &uid
		authorName = u.Username
	}
	rs, err := content.SetRating(s.DB, articleID, userID, authorName, in.Score)
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
	voterKey := ""
	if u := CurrentUserFromContext(c); u != nil {
		voterKey = content.VoterKey(&u.ID, u.Username)
	}
	if voterKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需要登录。"})
		return
	}
	ok, err := content.DeleteRating(s.DB, articleID, voterKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "取消评分失败。"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到评分记录。"})
		return
	}
	// Return updated summary
	rs, err := content.GetRatingSummary(s.DB, articleID, voterKey)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	c.JSON(http.StatusOK, rs)
}

// GET /api/articles/{type}/{slug}/ratings
func (s *Server) listRatings(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	ratings, err := content.ListRatings(s.DB, articleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评分记录失败。"})
		return
	}
	if ratings == nil {
		ratings = []content.Rating{}
	}
	c.JSON(http.StatusOK, gin.H{"ratings": ratings})
}
