package api

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

var slugRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ── Article list ──────────────────────────────────────────────────────

// GET /api/articles?type=md&page=1&before=123
// `before` is a keyset cursor (article id); omit/0 for the first page. `page`
// is kept for display only — actual pagination is cursor-based (see
// content.ListArticles) to avoid O(offset) scans on deep pages.
func (s *Server) listArticles(c *gin.Context) {
	atype := strings.TrimSpace(c.Query("type"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	before, _ := strconv.Atoi(c.Query("before"))
	if before < 0 {
		before = 0
	}
	result, err := content.ListArticles(s.DB, atype, page, 10, before)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章列表失败。"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ── Article detail (aggregated: article + comments + line_comments + rating) ─

type ArticleDetail struct {
	Article           *content.Article       `json:"article"`
	HTML              string                 `json:"html"`
	Tags              []string               `json:"tags"`
	Comments          []content.Comment      `json:"comments"`
	LineComments      []content.Comment      `json:"line_comments"`
	LineCommentCounts map[int]int            `json:"line_comment_counts"`
	Rating            *content.RatingSummary `json:"rating"`
	CanEdit           bool                   `json:"can_edit"`
}

// GET /api/articles/{type}/{slug}
func (s *Server) getArticle(c *gin.Context) {
	atype := c.Param("type")
	slug := c.Param("slug")

	a, err := content.GetArticle(s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}

	// ETag / short-TTL cache for the aggregated detail (P3-28). The tag covers
	// only PUBLIC state (article id + last update), so it's safe to share
	// across users. Logged-in users see per-user fields (can_edit, their own
	// rating), so we never 304 for them and disable caching; anonymous reads
	// get a private 30s cache + conditional 304 on a matching If-None-Match.
	//
	// ETag is computed BEFORE the comments/line-comments/rating sub-queries so
	// a 304 response short-circuits the DB load. A cache hit shouldn't pay for
	// comments/ratings the client already has.
	currentUser := CurrentUserFromContext(c)
	etag := fmt.Sprintf("\"%d-%d\"", a.ID, a.UpdatedAt.Unix())
	c.Header("ETag", etag)
	if currentUser == nil {
		c.Header("Cache-Control", "private, max-age=30")
		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}
	} else {
		c.Header("Cache-Control", "no-store")
	}

	html := parsers.Render(parsers.ArticleType(atype), a.ID, a.Content)

	// Sub-query failures are now fail-fast: a partial response (article +
	// half-broken comments) is worse than a 500, since the frontend can't
	// distinguish "no comments" from "comments failed to load".
	comments, err := content.GetComments(s.DB, a.ID)
	if err != nil {
		slog.Error("getArticle: load comments", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评论失败。"})
		return
	}
	lineComments, err := content.GetLineComments(s.DB, a.ID)
	if err != nil {
		slog.Error("getArticle: load line comments", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论失败。"})
		return
	}
	lineCounts, err := content.GetLineCommentCounts(s.DB, a.ID)
	if err != nil {
		slog.Error("getArticle: load line comment counts", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论统计失败。"})
		return
	}

	voterKey := ""
	canEdit := false
	if currentUser != nil {
		voterKey = content.VoterKey(&currentUser.ID, currentUser.Username)
		// Admin/owner can edit; author can edit their own.
		if currentUser.Role == "admin" || currentUser.Role == "owner" ||
			(a.AuthorID != nil && *a.AuthorID == currentUser.ID) {
			canEdit = true
		}
	}
	rating, err := content.GetRatingSummary(s.DB, a.ID, voterKey)
	if err != nil {
		slog.Error("getArticle: load rating", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评分失败。"})
		return
	}

	c.JSON(http.StatusOK, ArticleDetail{
		Article:           a,
		HTML:              string(html),
		Tags:              a.Tags,
		Comments:          comments,
		LineComments:      lineComments,
		LineCommentCounts: lineCounts,
		Rating:            rating,
		CanEdit:           canEdit,
	})
}

// ── CRUD (admin only) ─────────────────────────────────────────────────

type articleInput struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// POST /api/articles (admin)
func (s *Server) createArticle(c *gin.Context) {
	atype := strings.TrimSpace(c.Query("type"))
	if !parsers.IsValidType(atype) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章类型。"})
		return
	}
	var in articleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Slug = strings.TrimSpace(in.Slug)
	in.Title = strings.TrimSpace(in.Title)
	if in.Slug == "" || in.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug 和标题不能为空。"})
		return
	}
	if !slugRe.MatchString(in.Slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug 只能包含字母、数字、连字符和下划线。"})
		return
	}
	var authorID *int
	if u := CurrentUserFromContext(c); u != nil {
		authorID = &u.ID
	}
	a, err := content.CreateArticle(s.DB, atype, in.Slug, in.Title, in.Content, authorID)
	if err != nil {
		if isDuplicateEntry(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "该 slug 已存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建文章失败。"})
		return
	}
	if len(in.Tags) > 0 {
		_ = content.SetArticleTags(s.DB, a.ID, in.Tags)
	}
	c.JSON(http.StatusCreated, a)
}

// PUT /api/articles/{type}/{slug} (admin)
func (s *Server) updateArticle(c *gin.Context) {
	atype := c.Param("type")
	slug := c.Param("slug")
	var in articleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空。"})
		return
	}
	a, err := content.UpdateArticle(s.DB, atype, slug, in.Title, in.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新文章失败。"})
		return
	}
	if in.Tags != nil {
		_ = content.SetArticleTags(s.DB, a.ID, in.Tags)
	}
	c.JSON(http.StatusOK, a)
}

// DELETE /api/articles/{type}/{slug} (admin)
func (s *Server) deleteArticle(c *gin.Context) {
	atype := c.Param("type")
	slug := c.Param("slug")
	ok, err := content.DeleteArticle(s.DB, atype, slug)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ── Labels ────────────────────────────────────────────────────────────

// GET /api/labels
func (s *Server) listLabels(c *gin.Context) {
	tags, err := content.GetAllTagsWithCounts(s.DB)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载标签失败。"})
		return
	}
	c.JSON(http.StatusOK, tags)
}

// GET /api/labels/{tag}
func (s *Server) getLabelArticles(c *gin.Context) {
	tagName := c.Param("tag")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	result, err := content.GetArticlesByTag(s.DB, tagName, page, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载失败。"})
		return
	}
	c.JSON(http.StatusOK, result)
}
