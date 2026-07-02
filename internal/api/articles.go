package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	htmlpkg "html"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"

	"gokych/internal/auth/user"
	"gokych/internal/content"
	"gokych/internal/content/parsers"
	coredb "gokych/internal/core/db"
	"gokych/internal/typst"
)

// slugRe defines INVALID characters that must NOT appear in a slug.
// We use a blacklist approach to allow any Unicode characters
// (including Chinese, Japanese, emoji, spaces, etc.) while only
// rejecting characters that are dangerous in URL paths or filenames:
//   - ASCII control chars (0x00-0x1F, 0x7F)
//   - Forward slash / (path separator)
//   - Backslash \ (Windows path separator)
//   - Null byte \x00
var slugRe = regexp.MustCompile(`[\x00-\x1F\x7F/\\]`)

// maxSlugRunes caps slug length. MySQL utf8mb4 + VARCHAR(255) holds up to
// 255 chars; we cap at 128 runes so the URL stays reasonably short and a
// future filename derived from the slug (typst_files, Content-Disposition)
// can't blow past common filesystem name limits (~255 bytes).
const maxSlugRunes = 128

// ── Article list ──────────────────────────────────────────────────────

// GET /api/articles?type=md&page=1&before=123&author_id=42
// `before` is a keyset cursor (article id); omit/0 for the first page. `page`
// is kept for display only — actual pagination is cursor-based (see
// content.ListArticles) to avoid O(offset) scans on deep pages. `author_id`
// filters the list to articles by a specific user; the regular "我的文章"
// view on /admin/articles passes the caller's own id here so non-admin users
// only see what they authored.
func (s *Server) listArticles(c *gin.Context) {
	ctx := c.Request.Context()
	atype := strings.TrimSpace(c.Query("type"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	before, _ := strconv.Atoi(c.Query("before"))
	if before < 0 {
		before = 0
	}
	var authorID *int
	if a := c.Query("author_id"); a != "" {
		if v, err := strconv.Atoi(a); err == nil && v > 0 {
			authorID = &v
		}
	}
	result, err := content.ListArticlesCtx(ctx, s.DB, atype, authorID, page, 10, before)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章列表失败。"})
		return
	}
	currentUser := CurrentUserFromContext(c)
	if currentUser == nil {
		c.Header("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	} else {
		c.Header("Cache-Control", "private, max-age=30")
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
	CompileStatus     *typst.CompileStatus   `json:"compile_status,omitempty"`
}

// GET /api/articles/{type}/{slug}
func (s *Server) getArticle(c *gin.Context) {
	ctx := c.Request.Context()
	atype := c.Param("type")
	slug := c.Param("slug")

	a, err := content.GetArticleCtx(ctx, s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}

	// ETag is based on article id + last update. Cached HTML is the public/anon
	// view (identical for all users), so even logged-in visitors can get a 304
	// when the article hasn't changed. Personalised fields (can_edit, user
	// rating) are in separate JSON fields and don't affect the ETag.
	currentUser := CurrentUserFromContext(c)
	etag := fmt.Sprintf("\"%d-%d\"", a.ID, a.UpdatedAt.Unix())
	c.Header("ETag", etag)
	if currentUser == nil {
		// Anonymous: allow CDN/Edge caching since HTML is identical for all.
		// 5 min fresh + 1 hour stale-while-revalidate for good CDN hit ratio
		// while keeping content reasonably up-to-date.
		c.Header("Cache-Control", "public, max-age=300, stale-while-revalidate=3600")
	} else {
		// Logged-in: private cache with short freshness + short SWR.
		c.Header("Cache-Control", "private, max-age=30, stale-while-revalidate=60")
	}
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}

	// Compute voter key first (needed for rating query).
	voterKey := ""
	if currentUser != nil {
		voterKey = content.VoterKey(&currentUser.ID, currentUser.Username)
	}

	// Parallel fetch of independent DB queries: rating, comments, lineComments,
	// lineCounts, and (for typst articles) the initial compile status.
	var (
		rating           *content.RatingSummary
		comments         []content.Comment
		lineComments     []content.Comment
		lineCounts       map[int]int
		typstCS          *typst.CompileStatus
		ratingErr        error
		commentsErr      error
		lineCommentsErr  error
		lineCountsErr    error
		typstCSErr       error
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		rating, ratingErr = content.GetRatingSummaryCtx(gctx, s.DB, a.ID, voterKey)
		return ratingErr
	})
	g.Go(func() error {
		comments, commentsErr = content.GetCommentsCtx(gctx, s.DB, a.ID)
		return commentsErr
	})
	g.Go(func() error {
		lineComments, lineCommentsErr = content.GetLineCommentsCtx(gctx, s.DB, a.ID)
		return lineCommentsErr
	})
	g.Go(func() error {
		lineCounts, lineCountsErr = content.GetLineCommentCountsCtx(gctx, s.DB, a.ID)
		return lineCountsErr
	})
	if atype == "typst" {
		g.Go(func() error {
			typstCS, typstCSErr = s.Typst.GetCompileStatusCtx(gctx, a.ID)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		if errors.Is(ratingErr, err) {
			slog.Error("getArticle: load rating", "article_id", a.ID, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评分失败。"})
		} else if errors.Is(commentsErr, err) {
			slog.Error("getArticle: load comments", "article_id", a.ID, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评论失败。"})
		} else if errors.Is(lineCommentsErr, err) {
			slog.Error("getArticle: load line comments", "article_id", a.ID, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论失败。"})
		} else if errors.Is(lineCountsErr, err) {
			slog.Error("getArticle: load line comment counts", "article_id", a.ID, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论统计失败。"})
		} else {
			slog.Error("getArticle: parallel fetch", "article_id", a.ID, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章数据失败。"})
		}
		return
	}

	// Determine the article HTML: prefer pre-rendered cache (zero CPU cost),
	// fall back to live rendering if cache is empty (shouldn't happen for
	// non-typst articles post-WarmCache, but handles the race where an
	// article was just created and the render hasn't finished yet).
	var html template.HTML
	var typstShowCompileMsg bool
	var typstCompileErr string
	var compileStatus *typst.CompileStatus

	if atype == "typst" {
		if typstCSErr != nil {
			slog.Error("getArticle: typst compile status", "article_id", a.ID, "err", typstCSErr)
		} else if typstCS != nil {
			compileStatus = typstCS
			switch typstCS.Status {
			case "pending", "compiling":
				typstShowCompileMsg = true
			case "failed":
				typstShowCompileMsg = true
				typstCompileErr = typstCS.ErrorMessage
			}
		} else {
			if typst.Available() {
				if _, cacheErr := s.Typst.CompileHTMLCachedCtx(ctx, a.ID, ""); cacheErr != nil {
					slog.Info("getArticle: typst cache miss with no queue, auto-enqueuing",
						"article_id", a.ID)
					if qerr := s.Typst.EnqueueCompileCtx(ctx, a.ID); qerr != nil {
						slog.Warn("getArticle: auto-enqueue failed", "article_id", a.ID, "err", qerr)
					} else {
						typstShowCompileMsg = true
						if freshCs, freshErr := s.Typst.GetCompileStatusCtx(ctx, a.ID); freshErr == nil {
							compileStatus = freshCs
						}
					}
				}
			}
		}
	}

	if a.RenderedHTML != "" && !typstShowCompileMsg {
		html = template.HTML(a.RenderedHTML)
	} else {
		lookup := &wikidotPageLookup{
			ctx:          ctx,
			db:           s.DB,
			currentType:  a.Type,
			currentSlug:  a.Slug,
		}
		if currentUser != nil {
			lookup.currentUserID = &currentUser.ID
		}
		userLookup := &wikidotUserLookup{ctx: ctx, db: s.DB}
		vars := buildArticleVars(a, currentUser, rating)
		renderCtx := &parsers.RenderContext{
			Ctx:         ctx,
			PageLookup:  lookup,
			UserLookup:  userLookup,
			Vars:        vars,
			ArticleType: a.Type,
			Typst:       s.Typst,
		}
		html = parsers.RenderCtx(parsers.ArticleType(atype), a.ID, a.Content, renderCtx)
		if atype != "typst" && a.RenderedHTML == "" {
			go func() {
				bgCtx := context.Background()
				if err := content.RenderAndSaveCtx(bgCtx, s.DB, s.Typst, a); err != nil {
					slog.Warn("getArticle: async cache fill failed", "article_id", a.ID, "err", err)
				}
			}()
		}
	}

	html = template.HTML(s.rewriteStaticAssetURLs(string(html)))

	if atype == "typst" && typstShowCompileMsg {
		if typstCompileErr != "" {
			errMsg := htmlpkg.EscapeString(typstCompileErr)
			html = template.HTML(`<div class="typst-compile-failed" style="padding:2rem;border:1px solid #f56565;border-radius:8px;background:#fff5f5;color:#c53030;"><strong>❌ Typst 编译失败</strong><br><pre style="white-space:pre-wrap;margin-top:0.5rem;font-size:0.875rem;">` + errMsg + `</pre></div>`)
		} else {
			html = template.HTML(`<div class="typst-compiling" style="padding:2rem;text-align:center;color:#666;"><em>⏳ Typst 文档正在编译中，请稍后刷新页面查看...</em></div>`)
		}
	}

	s.renderCommentHTML(comments)
	s.renderCommentHTML(lineComments)

	canEdit := false
	if currentUser != nil {
		if currentUser.Role == "admin" || currentUser.Role == "owner" ||
			(a.AuthorID != nil && *a.AuthorID == currentUser.ID) {
			canEdit = true
		}
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
		CompileStatus:     compileStatus,
	})
}

// ── CRUD (any logged-in user; update/delete require admin/owner OR author) ──

type articleInput struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// canModifyArticle reports whether u is allowed to edit/delete the article.
// Admin/owner always can; otherwise only the original author can. An article
// with no author (created before author_id was tracked) can only be touched
// by admin/owner — we don't want a regular user to be able to claim an
// orphan row by getting its id into their session.
func canModifyArticle(u *user.User, a *content.Article) bool {
	if u == nil || a == nil {
		return false
	}
	if user.IsAdmin(u.Role) {
		return true
	}
	return a.AuthorID != nil && *a.AuthorID == u.ID
}

// POST /api/articles (any logged-in user)
func (s *Server) createArticle(c *gin.Context) {
	ctx := c.Request.Context()
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
	if slugRe.MatchString(in.Slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug 不能包含路径分隔符（/、\\）或控制字符。"})
		return
	}
	if len([]rune(in.Slug)) > maxSlugRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug 过长（最多 128 个字符）。"})
		return
	}
	// The charset above already excludes "/" and "\\", but a slug made of
	// dots alone ("." / "..") would still look like a relative-segment
	// traversal in URL assembly; reject them explicitly so /md/.. can
	// never be confused with a parent path.
	if in.Slug == "." || in.Slug == ".." {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slug 不能为 \".\" 或 \"..\"。"})
		return
	}
	// Author is the caller's id; requireLogin on the route group guarantees
	// u != nil here, but a defensive check keeps the linter quiet and makes
	// the invariant explicit.
	var authorID *int
	if u := CurrentUserFromContext(c); u != nil {
		authorID = &u.ID
	}
	a, err := content.CreateArticleCtx(ctx, s.DB, s.Typst, atype, in.Slug, in.Title, in.Content, authorID)
	if err != nil {
		if coredb.IsDuplicateEntry(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "该 slug 已存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建文章失败。"})
		return
	}
	// Typst articles are compiled asynchronously via the background worker
	// (EnqueueCompile was already called inside content.CreateArticle). We
	// return the article immediately with a compile_status of "pending" so
	// the editor can show a "compiling..." indicator; the page will reflect
	// the compiled output once the worker finishes.
	if len(in.Tags) > 0 {
		_ = content.SetArticleTagsCtx(ctx, s.DB, s.Typst, a.ID, in.Tags)
	}
	// Attach compile status for the response
	if atype == "typst" {
		if cs, err := s.Typst.GetCompileStatusCtx(ctx, a.ID); err == nil && cs != nil {
			c.JSON(http.StatusCreated, gin.H{"article": a, "compile_status": cs})
			return
		}
	}
	c.JSON(http.StatusCreated, a)
}

// PUT /api/articles/{type}/{slug} (admin/owner OR the article's author)
func (s *Server) updateArticle(c *gin.Context) {
	ctx := c.Request.Context()
	atype := c.Param("type")
	slug := c.Param("slug")

	// Load the article to do the ownership check. One extra SELECT beats
	// bolting role/author into the UPDATE WHERE clause, which would force
	// us to leak auth context into content-layer SQL.
	a, err := content.GetArticleCtx(ctx, s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}
	if !canModifyArticle(CurrentUserFromContext(c), a) {
		c.JSON(http.StatusForbidden, gin.H{"error": "您没有权限编辑此文章。"})
		return
	}

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
	a, err = content.UpdateArticleCtx(ctx, s.DB, s.Typst, atype, slug, in.Title, in.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新文章失败。"})
		return
	}
	// Typst articles are re-compiled asynchronously by the background worker
	// (EnqueueCompile was already called inside content.UpdateArticle). The
	// old cache is deleted so readers see a "compiling..." placeholder
	// instead of stale HTML until the worker finishes. No rollback on
	// failure — compile errors are surfaced in the compile_status field
	// and the admin UI shows them to the editor.
	if in.Tags != nil {
		_ = content.SetArticleTagsCtx(ctx, s.DB, s.Typst, a.ID, in.Tags)
	}
	if atype == "typst" {
		if cs, err := s.Typst.GetCompileStatusCtx(ctx, a.ID); err == nil && cs != nil {
			c.JSON(http.StatusOK, gin.H{"article": a, "compile_status": cs})
			return
		}
	}
	c.JSON(http.StatusOK, a)
}

// DELETE /api/articles/{type}/{slug} (admin/owner OR the article's author)
func (s *Server) deleteArticle(c *gin.Context) {
	ctx := c.Request.Context()
	atype := c.Param("type")
	slug := c.Param("slug")

	a, err := content.GetArticleCtx(ctx, s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}
	if !canModifyArticle(CurrentUserFromContext(c), a) {
		c.JSON(http.StatusForbidden, gin.H{"error": "您没有权限删除此文章。"})
		return
	}

	ok, err := content.DeleteArticleCtx(ctx, s.DB, s.Typst, atype, slug)
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
	ctx := c.Request.Context()
	tags, err := content.GetAllTagsWithCountsCtx(ctx, s.DB)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载标签失败。"})
		return
	}
	c.Header("Cache-Control", "public, max-age=300, stale-while-revalidate=3600")
	c.JSON(http.StatusOK, tags)
}

// GET /api/labels/{tag}
func (s *Server) getLabelArticles(c *gin.Context) {
	ctx := c.Request.Context()
	tagName := c.Param("tag")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	result, err := content.GetArticlesByTagCtx(ctx, s.DB, tagName, page, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载失败。"})
		return
	}
	currentUser := CurrentUserFromContext(c)
	if currentUser == nil {
		c.Header("Cache-Control", "public, max-age=60, stale-while-revalidate=300")
	} else {
		c.Header("Cache-Control", "private, max-age=30")
	}
	c.JSON(http.StatusOK, result)
}

// GET /api/articles/{type}/{slug}/compile-status
// Returns the current async compilation status for typst articles. Used by
// the frontend to poll for progress and auto-refresh when compilation finishes.
func (s *Server) getCompileStatus(c *gin.Context) {
	ctx := c.Request.Context()
	atype := c.Param("type")
	slug := c.Param("slug")
	if atype != "typst" {
		c.JSON(http.StatusOK, gin.H{"status": "not_applicable"})
		return
	}
	a, err := content.GetArticleCtx(ctx, s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}
	cs, err := s.Typst.GetCompileStatusCtx(ctx, a.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询编译状态失败。"})
		return
	}
	if cs == nil {
		// No queue entry — check if cache exists (success state without queue row)
		if _, err := s.Typst.CompileHTMLCachedCtx(ctx, a.ID, ""); err == nil {
			c.JSON(http.StatusOK, gin.H{"status": "success"})
			return
		}
		// Enqueue for compilation if neither queue nor cache exists.
		if !typst.Available() {
			c.JSON(http.StatusOK, gin.H{"status": "failed", "error_message": "Typst 编译器未安装"})
			return
		}
		if qerr := s.Typst.EnqueueCompileCtx(ctx, a.ID); qerr != nil {
			slog.Warn("getCompileStatus: auto-enqueue failed", "article_id", a.ID, "err", qerr)
		}
		c.JSON(http.StatusOK, gin.H{"status": "pending"})
		return
	}
	c.JSON(http.StatusOK, cs)
}

// POST /api/articles/{type}/{slug}/recompile
// Manually triggers a re-compile for a typst article (admin/owner or author).
// Useful after fixing a syntax error, or to force a refresh after a dependency
// update that the cascade missed.
func (s *Server) recompileArticle(c *gin.Context) {
	ctx := c.Request.Context()
	atype := c.Param("type")
	slug := c.Param("slug")
	if atype != "typst" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有 Typst 文章支持重新编译。"})
		return
	}
	a, err := content.GetArticleCtx(ctx, s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}
	if !canModifyArticle(CurrentUserFromContext(c), a) {
		c.JSON(http.StatusForbidden, gin.H{"error": "您没有权限重新编译此文章。"})
		return
	}
	if !typst.Available() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Typst 编译器未安装。"})
		return
	}
	// Delete old cache and enqueue fresh compilation.
	if _, derr := s.DB.ExecContext(ctx, `DELETE FROM typst_cache WHERE article_id = ?`, a.ID); derr != nil {
		slog.Warn("recompileArticle: failed to invalidate old cache", "article_id", a.ID, "err", derr)
	}
	if err := s.Typst.EnqueueCompileCtx(ctx, a.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交编译任务失败：" + err.Error()})
		return
	}
	cs, _ := s.Typst.GetCompileStatusCtx(ctx, a.ID)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "compile_status": cs})
}
