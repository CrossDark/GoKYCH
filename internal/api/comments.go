package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
)

// GET /api/articles/{type}/{slug}/comments
func (s *Server) listComments(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	comments, err := content.GetComments(s.DB, articleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评论失败。"})
		return
	}
	if comments == nil {
		comments = []content.Comment{}
	}
	c.JSON(http.StatusOK, comments)
}

// POST /api/articles/{type}/{slug}/comments
func (s *Server) addComment(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	var in struct {
		Content    string `json:"content"`
		AuthorName string `json:"author_name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "评论内容不能为空。"})
		return
	}
	// Logged-in users: bind to session id and use the session username as the
	// display name (ignoring any client-supplied author_name, which would
	// otherwise allow impersonation). Anonymous users keep the supplied name.
	var userID *int
	authorName := in.AuthorName
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		userID = &uid
		authorName = u.Username
	}
	cm, err := content.AddComment(s.DB, articleID, userID, authorName, in.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加评论失败。"})
		return
	}
	c.JSON(http.StatusCreated, cm)
}

// GET /api/articles/{type}/{slug}/line-comments
func (s *Server) listLineComments(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	lnStr := c.Query("line")
	if lnStr != "" {
		ln, _ := strconv.Atoi(lnStr)
		comments, err := content.GetLineCommentsByLine(s.DB, articleID, ln)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论失败。"})
			return
		}
		if comments == nil {
			comments = []content.Comment{}
		}
		c.JSON(http.StatusOK, comments)
		return
	}
	comments, err := content.GetLineComments(s.DB, articleID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论失败。"})
		return
	}
	if comments == nil {
		comments = []content.Comment{}
	}
	c.JSON(http.StatusOK, comments)
}

// POST /api/articles/{type}/{slug}/line-comments
func (s *Server) addLineComment(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	var in struct {
		LineNumber int    `json:"line_number"`
		Content    string `json:"content"`
		AuthorName string `json:"author_name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "内容不能为空。"})
		return
	}
	var userID *int
	authorName := in.AuthorName
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		userID = &uid
		authorName = u.Username
	}
	cm, err := content.AddLineComment(s.DB, articleID, in.LineNumber, userID, authorName, in.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加行评论失败。"})
		return
	}
	c.JSON(http.StatusCreated, cm)
}

// articleIDFromParams resolves the article ID from the {type}/{slug} path params.
func (s *Server) articleIDFromParams(c *gin.Context) (int, error) {
	atype := c.Param("type")
	slug := c.Param("slug")
	a, err := content.GetArticle(s.DB, atype, slug)
	if err != nil {
		return 0, err
	}
	return a.ID, nil
}
