package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gokych/internal/core/themes"
)

// GET /api/themes — public list of installed themes (name + meta + has_css).
// Used by the admin settings page to populate the theme dropdown.
func (s *Server) listThemes(c *gin.Context) {
	out, err := themes.List(s.DataDir)
	if err != nil {
		slog.Error("listThemes", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载主题失败。"})
		return
	}
	if out == nil {
		out = []themes.Theme{}
	}
	c.JSON(http.StatusOK, out)
}

// GET /api/themes/:name — serve the theme's static/theme.css as
// `text/css`. The :name segment is regex-validated inside themes.ReadCSS so
// a path like `../../etc` is rejected before any FS access.
//
// Returns 404 if the theme directory or theme.css doesn't exist so the
// layout can silently fall back to the built-in globals.css.
func (s *Server) getThemeCSS(c *gin.Context) {
	name := c.Param("name")
	if !themes.ValidateName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的主题名。"})
		return
	}
	css, err := themes.ReadCSS(s.DataDir, name)
	if err != nil {
		slog.Error("getThemeCSS", "name", name, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取主题失败。"})
		return
	}
	if css == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "主题不存在。"})
		return
	}
	// Cache aggressively: theme files are content-named (the directory is
	// the version), so the URL itself is the cache key. Admin edits create
	// a new directory under data/themes/ and switch the setting, so old
	// URLs naturally drop out of the browser cache.
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/css; charset=utf-8", css)
}
