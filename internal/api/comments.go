package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

const (
	commentContentMaxLen = 500
	lineCommentMaxLen    = 20
)

func (s *Server) listComments(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	comments, err := content.GetCommentsCtx(ctx, s.DB, articleID)
	if err != nil {
		respondInternalErr(c, "加载评论失败。")
		return
	}
	if comments == nil {
		comments = []content.Comment{}
	}
	s.renderCommentHTML(comments)
	c.JSON(http.StatusOK, comments)
}

func (s *Server) addComment(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	var in struct {
		Content    string `json:"content"`
		AuthorName string `json:"author_name"`
	}
	if !bindJSON(c, &in) {
		return
	}
	in.Content, ok = validateLen(c, in.Content, "评论内容", commentContentMaxLen)
	if !ok {
		return
	}
	var userID *int
	authorName := in.AuthorName
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		userID = &uid
		authorName = u.Username
	}
	ctx := c.Request.Context()
	cm, err := content.AddCommentCtx(ctx, s.DB, articleID, userID, authorName, in.Content)
	if err != nil {
		respondInternalErr(c, "添加评论失败。")
		return
	}
	cm.ContentHTML = s.rewriteStaticAssetURLs(parsers.RenderSafeMarkdown(cm.Content))
	c.JSON(http.StatusCreated, cm)
}

func (s *Server) listLineComments(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	lnStr := c.Query("line")
	if lnStr != "" {
		ln, _ := strconv.Atoi(lnStr)
		comments, err := content.GetLineCommentsByLineCtx(ctx, s.DB, articleID, ln)
		if err != nil {
			respondInternalErr(c, "加载行评论失败。")
			return
		}
		if comments == nil {
			comments = []content.Comment{}
		}
		s.renderCommentHTML(comments)
		c.JSON(http.StatusOK, comments)
		return
	}
	comments, err := content.GetLineCommentsCtx(ctx, s.DB, articleID)
	if err != nil {
		respondInternalErr(c, "加载行评论失败。")
		return
	}
	if comments == nil {
		comments = []content.Comment{}
	}
	s.renderCommentHTML(comments)
	c.JSON(http.StatusOK, comments)
}

func (s *Server) addLineComment(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	var in struct {
		LineNumber int    `json:"line_number"`
		Content    string `json:"content"`
		AuthorName string `json:"author_name"`
	}
	if !bindJSON(c, &in) {
		return
	}
	in.Content, ok = validateLen(c, in.Content, "行评论", lineCommentMaxLen)
	if !ok {
		return
	}
	if in.LineNumber < 0 {
		respondBadRequest(c, "行号不能为负数。")
		return
	}
	var userID *int
	authorName := in.AuthorName
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		userID = &uid
		authorName = u.Username
	}
	ctx := c.Request.Context()
	cm, err := content.AddLineCommentCtx(ctx, s.DB, articleID, in.LineNumber, userID, authorName, in.Content)
	if err != nil {
		respondInternalErr(c, "添加行评论失败。")
		return
	}
	cm.ContentHTML = s.rewriteStaticAssetURLs(parsers.RenderSafeMarkdown(cm.Content))
	c.JSON(http.StatusCreated, cm)
}

func (s *Server) renderCommentHTML(comments []content.Comment) {
	for i := range comments {
		html := parsers.RenderSafeMarkdown(comments[i].Content)
		if s != nil {
			html = s.rewriteStaticAssetURLs(html)
		}
		comments[i].ContentHTML = html
	}
}

func (s *Server) articleIDFromParams(c *gin.Context) (int, error) {
	atype := c.Param("type")
	slug := c.Param("slug")
	if !parsers.IsValidType(atype) {
		return 0, errInvalidArticleType
	}
	ctx := c.Request.Context()
	a, err := content.GetArticleCtx(ctx, s.DB, atype, slug)
	if err != nil {
		return 0, err
	}
	return a.ID, nil
}
