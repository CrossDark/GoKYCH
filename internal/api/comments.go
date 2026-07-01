package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

// Max comment lengths, kept in sync with the schema's column widths.
const (
	commentContentMaxLen = 500 // schema: comments.content VARCHAR(500)
	lineCommentMaxLen    = 20  // line comments are 20 chars by design
)

// GET /api/articles/{type}/{slug}/comments
func (s *Server) listComments(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		if err == errInvalidArticleType {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章类型。"})
			return
		}
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
	renderCommentHTML(comments)
	c.JSON(http.StatusOK, comments)
}

// POST /api/articles/{type}/{slug}/comments
func (s *Server) addComment(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		if err == errInvalidArticleType {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章类型。"})
			return
		}
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
	if len([]rune(in.Content)) > commentContentMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("评论内容不能超过 %d 个字符。", commentContentMaxLen)})
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
	cm.ContentHTML = parsers.RenderSafeMarkdown(cm.Content)
	c.JSON(http.StatusCreated, cm)
}

// GET /api/articles/{type}/{slug}/line-comments
func (s *Server) listLineComments(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		if err == errInvalidArticleType {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章类型。"})
			return
		}
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
		renderCommentHTML(comments)
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
	renderCommentHTML(comments)
	c.JSON(http.StatusOK, comments)
}

// POST /api/articles/{type}/{slug}/line-comments
func (s *Server) addLineComment(c *gin.Context) {
	articleID, err := s.articleIDFromParams(c)
	if err != nil {
		if err == errInvalidArticleType {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章类型。"})
			return
		}
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
	if len([]rune(in.Content)) > lineCommentMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("行评论不能超过 %d 个字符。", lineCommentMaxLen)})
		return
	}
	if in.LineNumber < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "行号不能为负数。"})
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
	cm.ContentHTML = parsers.RenderSafeMarkdown(cm.Content)
	c.JSON(http.StatusCreated, cm)
}

// renderCommentHTML populates ContentHTML on every comment in the slice.
// Modifies the slice in place. Safe to call with nil/empty input.
func renderCommentHTML(comments []content.Comment) {
	for i := range comments {
		comments[i].ContentHTML = parsers.RenderSafeMarkdown(comments[i].Content)
	}
}

// errInvalidArticleType is returned by articleIDFromParams when the {type}
// path param doesn't match any known article type. Callers should translate
// this into HTTP 400 (vs the 404 returned for missing articles).
var errInvalidArticleType = fmt.Errorf("invalid article type")

// articleIDFromParams resolves the article ID from the {type}/{slug} path params.
// It validates the article type upfront so a malformed type returns a clean 400
// instead of silently hitting the DB and bouncing back as a 404.
func (s *Server) articleIDFromParams(c *gin.Context) (int, error) {
	atype := c.Param("type")
	slug := c.Param("slug")
	if !parsers.IsValidType(atype) {
		return 0, errInvalidArticleType
	}
	a, err := content.GetArticle(s.DB, atype, slug)
	if err != nil {
		return 0, err
	}
	return a.ID, nil
}
