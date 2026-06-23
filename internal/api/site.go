package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
	"gokych/internal/core/settings"
)

// ── Site info ─────────────────────────────────────────────────────────

// GET /api/site — returns site metadata read from settings.yml (data/settings/
// settings.yml), falling back to defaults on read/parse failure. Replaces the
// earlier hard-coded title/subtitle/language, which ignored admin edits made
// via /admin/settings.
func (s *Server) getSite(c *gin.Context) {
	cfg := settings.Load(s.DataDir)
	c.JSON(http.StatusOK, gin.H{
		"title":    settings.SiteValue(cfg, "title", "跨越晨昏"),
		"subtitle": settings.SiteValue(cfg, "subtitle", "个人网站"),
		"language": settings.SiteValue(cfg, "language", "zh-CN"),
	})
}

// ── Home ──────────────────────────────────────────────────────────────

// GET /api/home
func (s *Server) getHome(c *gin.Context) {
	// Subsite links.
	type subsiteLink struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	subLinks := []subsiteLink{}
	rows, err := s.DB.Query(`SELECT name, url, description FROM subsite_links ORDER BY sort_order`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var l subsiteLink
			if err := rows.Scan(&l.Name, &l.URL, &l.Description); err == nil {
				subLinks = append(subLinks, l)
			}
		}
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
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var f featuredArticle
			if err := rows2.Scan(&f.ID, &f.Type, &f.Slug, &f.Title); err == nil {
				featured = append(featured, f)
			}
		}
	}

	// Recent articles.
	recent, err := content.ListRecentArticles(s.DB, 10)
	if err != nil {
		recent = []content.Article{}
	}

	// Notifications.
	type notification struct {
		ID          int       `json:"id"`
		Title       string    `json:"title"`
		Content     string    `json:"content"`
		IsImportant bool      `json:"is_important"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	notifs := []notification{}
	rows3, err := s.DB.Query(
		`SELECT id, title, content, is_important, updated_at
		 FROM notifications WHERE is_active = 1 ORDER BY updated_at DESC`)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var n notification
			var imp int
			if err := rows3.Scan(&n.ID, &n.Title, &n.Content, &imp, &n.UpdatedAt); err == nil {
				n.IsImportant = imp == 1
				notifs = append(notifs, n)
			}
		}
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
		notifs = append(notifs, n)
	}
	c.JSON(http.StatusOK, notifs)
}
