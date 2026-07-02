package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
)

// GET /api/search?q=keyword&page=1
func (s *Server) search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索关键词不能为空。"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	result, err := content.SearchArticlesCtx(c.Request.Context(), s.DB, q, page, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "搜索失败。"})
		return
	}
	c.JSON(http.StatusOK, result)
}
