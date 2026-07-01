package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
	"gokych/internal/core/settings"
)

// ── Site info ─────────────────────────────────────────────────────────

// GET /api/site
//
// Returns the public-facing site config (site + appearance + features from
// settings.yml) plus subsite_links so the Header can render nav and footer
// ICP without a second round-trip. Per-user social links used to live in a
// `social` section here; they've been moved to per-user fields (see
// user.User) and are now exposed through /api/admin/profile and any future
// author-card endpoint. Settings read failures degrade gracefully to
// defaults — a broken YAML shouldn't 500 the home page.
func (s *Server) getSite(c *gin.Context) {
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		slog.Warn("getSite: settings.Load failed; serving defaults", "err", err)
		cfg = settings.Default()
	}

	type subsiteLink struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	subLinks := []subsiteLink{}
	rows, err := s.DB.Query(`SELECT name, url, description FROM subsite_links ORDER BY sort_order`)
	if err != nil {
		slog.Warn("getSite: subsite_links query failed", "err", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var l subsiteLink
			if err := rows.Scan(&l.Name, &l.URL, &l.Description); err == nil {
				subLinks = append(subLinks, l)
			}
		}
		if err := rows.Err(); err != nil {
			slog.Warn("getSite: iterate subsite_links rows failed", "err", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"site":          cfg["site"],
		"appearance":    cfg["appearance"],
		"features":      cfg["features"],
		"subsite_links": subLinks,
	})
}

// ── Home ──────────────────────────────────────────────────────────────

// GET /api/home
//
// Aggregates everything the homepage needs in one round-trip:
//   - subsite_links    — admin-curated nav links
//   - featured_articles — homepage highlights
//   - recent_articles  — fallback when no featured items exist
//   - notifications    — active ones, important first
//
// Each sub-query is now fail-fast: a DB error on any one returns 500 rather
// than a half-empty page that the frontend can't distinguish from a real
// empty state.
func (s *Server) getHome(c *gin.Context) {
	// Subsite links.
	type subsiteLink struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	subLinks := []subsiteLink{}
	rows, err := s.DB.Query(`SELECT name, url, description FROM subsite_links ORDER BY sort_order`)
	if err != nil {
		slog.Error("getHome: list subsite_links", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载子站点链接失败。"})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var l subsiteLink
		if err := rows.Scan(&l.Name, &l.URL, &l.Description); err == nil {
			subLinks = append(subLinks, l)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("getHome: iterate subsite_links rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载子站点链接失败。"})
		return
	}

	// Featured articles (with join to get title/type/slug).
	type featuredArticle struct {
		ID    int    `json:"id"`
		Type  string `json:"type"`
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	featured := []featuredArticle{}
	rows2, err := s.DB.Query(
		`SELECT a.id, a.type, a.slug, a.title
		 FROM featured_articles fa JOIN articles a ON fa.article_id = a.id
		 ORDER BY fa.sort_order`)
	if err != nil {
		slog.Error("getHome: list featured_articles", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载推荐文章失败。"})
		return
	}
	defer rows2.Close()
	for rows2.Next() {
		var f featuredArticle
		if err := rows2.Scan(&f.ID, &f.Type, &f.Slug, &f.Title); err == nil {
			featured = append(featured, f)
		}
	}
	if err := rows2.Err(); err != nil {
		slog.Error("getHome: iterate featured_articles rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载推荐文章失败。"})
		return
	}

	// Recent articles.
	recent, err := content.ListRecentArticles(s.DB, 10)
	if err != nil {
		slog.Error("getHome: list recent articles", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载最近文章失败。"})
		return
	}

	// Notifications.
	type notification struct {
		ID          int       `json:"id"`
		Title       string    `json:"title"`
		Content     string    `json:"content"`
		ContentHTML string    `json:"content_html"`
		IsImportant bool      `json:"is_important"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	notifs := []notification{}
	rows3, err := s.DB.Query(
		`SELECT id, title, content, is_important, updated_at
		 FROM notifications WHERE is_active = 1 ORDER BY is_important DESC, updated_at DESC`)
	if err != nil {
		slog.Error("getHome: list notifications", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载通知失败。"})
		return
	}
	defer rows3.Close()
	for rows3.Next() {
		var n notification
		var imp int
		if err := rows3.Scan(&n.ID, &n.Title, &n.Content, &imp, &n.UpdatedAt); err == nil {
			n.IsImportant = imp == 1
			n.ContentHTML = s.rewriteStaticAssetURLs(parsers.RenderSafeMarkdown(n.Content))
			notifs = append(notifs, n)
		}
	}
	if err := rows3.Err(); err != nil {
		slog.Error("getHome: iterate notifications rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载通知失败。"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"subsite_links":     subLinks,
		"featured_articles": featured,
		"recent_articles":   recent,
		"notifications":     notifs,
	})
}

// GET /api/notifications
func (s *Server) listNotifications(c *gin.Context) {
	type notif struct {
		ID          int       `json:"id"`
		Title       string    `json:"title"`
		Content     string    `json:"content"`
		ContentHTML string    `json:"content_html"`
		IsImportant bool      `json:"is_important"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	notifs := []notif{}
	rows, err := s.DB.Query(
		`SELECT id, title, content, is_important, updated_at
		 FROM notifications WHERE is_active = 1 ORDER BY updated_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载通知失败。"})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var n notif
		var imp int
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &imp, &n.UpdatedAt); err != nil {
			continue
		}
		n.IsImportant = imp == 1
		n.ContentHTML = s.rewriteStaticAssetURLs(parsers.RenderSafeMarkdown(n.Content))
		notifs = append(notifs, n)
	}
	if err := rows.Err(); err != nil {
		slog.Error("listNotifications: iterate rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载通知失败。"})
		return
	}
	c.JSON(http.StatusOK, notifs)
}
