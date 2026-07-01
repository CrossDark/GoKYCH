package api

import (
	"database/sql"
	"fmt"
	htmlpkg "html"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/user"
	"gokych/internal/content"
	"gokych/internal/content/parsers"
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
	result, err := content.ListArticles(s.DB, atype, authorID, page, 10, before)
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
	CompileStatus     *typst.CompileStatus   `json:"compile_status,omitempty"`
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

	// The vars injection needs the rating summary, so we
	// load it BEFORE the render. We do the load lazily here
	// (after the cheaper comments/line-comments queries that
	// come later in this handler) to keep the original
	// ordering of error reporting — a rating load failure
	// should still produce a 500, not silently omit
	// `%%rating%%` from the rendered output. voterKey is
	// empty for anonymous users; the rating summary ignores
	// it and returns the unfiltered average.
	voterKey := ""
	if currentUser != nil {
		voterKey = content.VoterKey(&currentUser.ID, currentUser.Username)
	}
	rating, err := content.GetRatingSummary(s.DB, a.ID, voterKey)
	if err != nil {
		slog.Error("getArticle: load rating", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评分失败。"})
		return
	}

	// Render with a context that wires up the page-lookup
	// adapter (for `[[include …]]` / `[[module ListPages]]`)
	// and the per-article `%%var%%` substitutions
	// (`%%user_name%%`, `%%title%%`, `%%tags%%`, `%%rating%%`,
	// `%%created_at%%`, etc.). For non-wikidot types the
	// context is a no-op (only the wikidot renderer reads it
	// today), so this stays free for md/bbcode/html.
	lookup := &wikidotPageLookup{
		db:          s.DB,
		currentType: a.Type,
		currentSlug: a.Slug,
	}
	if currentUser != nil {
		lookup.currentUserID = &currentUser.ID
	}
	// UserLookup is wired alongside PageLookup so the
	// renderer's `[[user Name]]` mentions resolve to the
	// users table — the link gets the user's nickname /
	// avatar / staff badge instead of the typed name.
	// The lookup is a thin DB-only adapter (no per-render
	// state) so a single instance is reused; constructing
	// it per render is still cheaper than the per-render
	// PageLookup allocation pattern.
	userLookup := &wikidotUserLookup{db: s.DB}
	vars := buildArticleVars(a, currentUser, rating)
	renderCtx := &parsers.RenderContext{
		PageLookup:  lookup,
		UserLookup:  userLookup,
		Vars:        vars,
		ArticleType: a.Type,
	}
	// For typst articles, check async compile status BEFORE rendering to
	// avoid a redundant cache lookup (renderTypst calls CompileHTMLCached
	// internally, but we need to know the status first to decide whether
	// to auto-enqueue, show a placeholder, or use cached HTML).
	//
	// Flow:
	//   1. If a queue row exists with pending/compiling → show "compiling…".
	//   2. If a queue row exists with failed → show error.
	//   3. If a queue row exists with success → render normally (cache hit).
	//   4. If NO queue row exists, check whether the cache has HTML:
	//      - cache hit → render normally (article was compiled before
	//        the queue system existed, or was compiled via recompile API).
	//      - cache miss → auto-enqueue the article for compilation and
	//        show "compiling…" so the first visitor triggers the build
	//        instead of staring at a permanent "not yet compiled" message.
	var typstShowCompileMsg bool
	var typstCompileErr string
	var compileStatus *typst.CompileStatus
	if atype == "typst" {
		cs, cerr := typst.GetCompileStatus(s.DB, a.ID)
		if cerr != nil {
			slog.Error("getArticle: typst compile status", "article_id", a.ID, "err", cerr)
		} else if cs != nil {
			compileStatus = cs
			switch cs.Status {
			case "pending", "compiling":
				typstShowCompileMsg = true
			case "failed":
				typstShowCompileMsg = true
				typstCompileErr = cs.ErrorMessage
			}
			// cs != nil → queue record exists; do NOT auto-enqueue here
			// (the worker is already handling it, or it permanently failed).
		} else {
			// cs == nil && cerr == nil: no queue record. Check whether the
			// cache has HTML. If not, auto-enqueue so the first visitor
			// triggers compilation instead of seeing a permanent
			// "not yet compiled" placeholder.
			if typst.Available() {
				if _, cacheErr := typst.CompileHTMLCached(a.ID, ""); cacheErr != nil {
					slog.Info("getArticle: typst cache miss with no queue, auto-enqueuing",
						"article_id", a.ID)
					if qerr := typst.EnqueueCompile(s.DB, a.ID); qerr != nil {
						slog.Warn("getArticle: auto-enqueue failed", "article_id", a.ID, "err", qerr)
					} else {
						typstShowCompileMsg = true
						// Re-fetch status after enqueuing so the response
						// includes the fresh 'pending' record.
						if freshCs, freshErr := typst.GetCompileStatus(s.DB, a.ID); freshErr == nil {
							compileStatus = freshCs
						}
					}
				}
			}
		}
	}

	html := parsers.RenderCtx(parsers.ArticleType(atype), a.ID, a.Content, renderCtx)
	// Rewrite /uploads/ and /avatars/ relative paths to absolute URLs
	// when PublicURL is set (cross-origin CDN deployments). Without
	// this, <img src="/uploads/foo.png"> in rendered article HTML
	// would resolve against the CDN origin which doesn't serve those
	// files (504/404). Dev (PublicURL="") passes through unchanged.
	html = template.HTML(s.rewriteStaticAssetURLs(string(html)))

	// Override HTML with compile-in-progress / error messages for typst
	// articles that aren't ready yet.
	if atype == "typst" && typstShowCompileMsg {
		if typstCompileErr != "" {
			errMsg := htmlpkg.EscapeString(typstCompileErr)
			html = template.HTML(`<div class="typst-compile-failed" style="padding:2rem;border:1px solid #f56565;border-radius:8px;background:#fff5f5;color:#c53030;"><strong>❌ Typst 编译失败</strong><br><pre style="white-space:pre-wrap;margin-top:0.5rem;font-size:0.875rem;">` + errMsg + `</pre></div>`)
		} else {
			html = template.HTML(`<div class="typst-compiling" style="padding:2rem;text-align:center;color:#666;"><em>⏳ Typst 文档正在编译中，请稍后刷新页面查看...</em></div>`)
		}
	}

	// Sub-query failures are now fail-fast: a partial response (article +
	// half-broken comments) is worse than a 500, since the frontend can't
	// distinguish "no comments" from "comments failed to load".
	comments, err := content.GetComments(s.DB, a.ID)
	if err != nil {
		slog.Error("getArticle: load comments", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载评论失败。"})
		return
	}
	s.renderCommentHTML(comments)
	lineComments, err := content.GetLineComments(s.DB, a.ID)
	if err != nil {
		slog.Error("getArticle: load line comments", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论失败。"})
		return
	}
	s.renderCommentHTML(lineComments)
	lineCounts, err := content.GetLineCommentCounts(s.DB, a.ID)
	if err != nil {
		slog.Error("getArticle: load line comment counts", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载行评论统计失败。"})
		return
	}

	// (voterKey was set earlier — before the render — so the
	// rating summary could be loaded with the viewer's score
	// for the `%%rating%%` substitution. canEdit is still
	// local to this block.)
	canEdit := false
	if currentUser != nil {
		// Admin/owner can edit; author can edit their own.
		if currentUser.Role == "admin" || currentUser.Role == "owner" ||
			(a.AuthorID != nil && *a.AuthorID == currentUser.ID) {
			canEdit = true
		}
	}
	// (rating was already loaded earlier, before the render, so
	// `%%rating%%` could be substituted into the wikidot vars
	// map. We reuse the existing local here.)

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
	a, err := content.CreateArticle(s.DB, atype, in.Slug, in.Title, in.Content, authorID)
	if err != nil {
		if isDuplicateEntry(err) {
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
		_ = content.SetArticleTags(s.DB, a.ID, in.Tags)
	}
	// Attach compile status for the response
	if atype == "typst" {
		if cs, err := typst.GetCompileStatus(s.DB, a.ID); err == nil && cs != nil {
			c.JSON(http.StatusCreated, gin.H{"article": a, "compile_status": cs})
			return
		}
	}
	c.JSON(http.StatusCreated, a)
}

// PUT /api/articles/{type}/{slug} (admin/owner OR the article's author)
func (s *Server) updateArticle(c *gin.Context) {
	atype := c.Param("type")
	slug := c.Param("slug")

	// Load the article to do the ownership check. One extra SELECT beats
	// bolting role/author into the UPDATE WHERE clause, which would force
	// us to leak auth context into content-layer SQL.
	a, err := content.GetArticle(s.DB, atype, slug)
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
	a, err = content.UpdateArticle(s.DB, atype, slug, in.Title, in.Content)
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
		_ = content.SetArticleTags(s.DB, a.ID, in.Tags)
	}
	if atype == "typst" {
		if cs, err := typst.GetCompileStatus(s.DB, a.ID); err == nil && cs != nil {
			c.JSON(http.StatusOK, gin.H{"article": a, "compile_status": cs})
			return
		}
	}
	c.JSON(http.StatusOK, a)
}

// DELETE /api/articles/{type}/{slug} (admin/owner OR the article's author)
func (s *Server) deleteArticle(c *gin.Context) {
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
	if !canModifyArticle(CurrentUserFromContext(c), a) {
		c.JSON(http.StatusForbidden, gin.H{"error": "您没有权限删除此文章。"})
		return
	}

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

// GET /api/articles/{type}/{slug}/compile-status
// Returns the current async compilation status for typst articles. Used by
// the frontend to poll for progress and auto-refresh when compilation finishes.
func (s *Server) getCompileStatus(c *gin.Context) {
	atype := c.Param("type")
	slug := c.Param("slug")
	if atype != "typst" {
		c.JSON(http.StatusOK, gin.H{"status": "not_applicable"})
		return
	}
	a, err := content.GetArticle(s.DB, atype, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文章失败。"})
		return
	}
	cs, err := typst.GetCompileStatus(s.DB, a.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询编译状态失败。"})
		return
	}
	if cs == nil {
		// No queue entry — check if cache exists (success state without queue row)
		if _, err := typst.CompileHTMLCached(a.ID, ""); err == nil {
			c.JSON(http.StatusOK, gin.H{"status": "success"})
			return
		}
		// Enqueue for compilation if neither queue nor cache exists.
		if !typst.Available() {
			c.JSON(http.StatusOK, gin.H{"status": "failed", "error_message": "Typst 编译器未安装"})
			return
		}
		if qerr := typst.EnqueueCompile(s.DB, a.ID); qerr != nil {
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
	atype := c.Param("type")
	slug := c.Param("slug")
	if atype != "typst" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只有 Typst 文章支持重新编译。"})
		return
	}
	a, err := content.GetArticle(s.DB, atype, slug)
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
	if _, derr := s.DB.Exec(`DELETE FROM typst_cache WHERE article_id = ?`, a.ID); derr != nil {
		slog.Warn("recompileArticle: failed to invalidate old cache", "article_id", a.ID, "err", derr)
	}
	if err := typst.EnqueueCompile(s.DB, a.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交编译任务失败：" + err.Error()})
		return
	}
	cs, _ := typst.GetCompileStatus(s.DB, a.ID)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "compile_status": cs})
}
