package api

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gokych/internal/core/settings"
	"gokych/internal/core/themes"
)

// MaxThemeUploadSize caps a single theme upload (zip or css) at 2 MB.
const MaxThemeUploadSize = 2 * 1024 * 1024

// GET /api/themes — public list of installed themes (name + meta + has_css + builtin).
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

// GET /api/themes/:name — serve the theme's static/theme.css as text/css.
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
	c.Header("Cache-Control", "public, max-age=3600, stale-while-revalidate=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/css; charset=utf-8", css)
}

// GET /api/themes/:name/assets/*filepath — serve any file under the theme's
// static/ subdirectory (currently used by the glass theme for particles.js).
// Path traversal is blocked in themes.ReadAsset; only whitelisted MIME types
// are served (other extensions return 400 so an attacker can't repurpose
// this endpoint to ship arbitrary content types).
func (s *Server) getThemeAsset(c *gin.Context) {
	name := c.Param("name")
	rel := c.Param("filepath")
	if !themes.ValidateName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的主题名。"})
		return
	}
	b, mime, err := themes.ReadAsset(s.DataDir, name, rel)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if b == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在。"})
		return
	}
	c.Header("Cache-Control", "public, max-age=3600, stale-while-revalidate=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, mime, b)
}

// GET /api/themes/:name/settings — public read of a theme's settings
// schema (from theme.yaml) merged with the admin's stored overrides
// (from theme_settings). The frontend uses this to render the admin
// "Settings" modal and the runtime glass-fx layer reads the EFFECTIVE
// values to decide which mode to render. Returned shape:
//
//	{
//	  "schema": [ { key, type, label, options, min, max, step, default, hint }, ... ],
//	  "values": { "effect_mode": "rain", "particle_density": "60", ... }
//	}
//
// `values` contains ONLY keys that have explicit admin overrides;
// anything missing falls back to the schema default at the call site.
// We also normalise each value to its string form (range → "60" not 60)
// so the client can treat them as an opaque string-to-string map.
func (s *Server) getThemeSettings(c *gin.Context) {
	name := c.Param("name")
	if !themes.ValidateName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的主题名。"})
		return
	}
	t, err := themes.Get(s.DataDir, name)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "主题不存在。"})
		return
	}
	overrides, err := themes.GetSettingsValues(s.DB, name)
	if err != nil {
		slog.Error("getThemeSettings: read overrides", "theme", name, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取主题设置失败。"})
		return
	}
	// Always return the schema (even if empty) and a values map; an
	// empty values map just means "use all defaults".
	if t.Settings == nil {
		t.Settings = []themes.SettingDefinition{}
	}
	if overrides == nil {
		overrides = map[string]string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"schema": t.Settings,
		"values": overrides,
	})
}

// PUT /api/admin/themes/:name/settings — owner-only write of admin
// overrides. Body: { "values": { "effect_mode": "rain", ... } }.
// Keys not declared in the theme's schema are rejected with 400.
// Range values are validated against min/max. Missing keys are NOT
// deleted (admin might just not be touching them).
func (s *Server) adminUpdateThemeSettings(c *gin.Context) {
	name := c.Param("name")
	if !themes.ValidateName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的主题名。"})
		return
	}
	t, err := themes.Get(s.DataDir, name)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "主题不存在。"})
		return
	}
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误。"})
		return
	}
	if body.Values == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 values 字段。"})
		return
	}
	rejects, err := themes.SetSettingsValues(s.DB, name, t.Settings, body.Values)
	if err != nil {
		// Reject list is the user-facing reason; the error string is the
		// technical cause. Both go back so the admin can see exactly
		// which keys were bad.
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "设置校验失败。",
			"rejects": rejects,
		})
		return
	}
	slog.Info("theme settings updated", "theme", name, "keys", len(body.Values))
	c.JSON(http.StatusOK, gin.H{"status": "ok", "updated": len(body.Values)})
}

// ── Admin-only theme management (owner-only) ────────────────────────

// GET /api/admin/themes — list themes with full metadata (admin panel).
func (s *Server) adminListThemes(c *gin.Context) {
	out, err := themes.List(s.DataDir)
	if err != nil {
		slog.Error("adminListThemes", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载主题列表失败。"})
		return
	}
	if out == nil {
		out = []themes.Theme{}
	}
	c.JSON(http.StatusOK, out)
}

// POST /api/admin/themes/upload — upload a theme (zip or css).
//
// Accepts multipart/form-data with either:
//   - "zip"  field: a .zip containing theme.yaml + static/theme.css
//   - "css"  field: a raw .css file (auto-generates theme.yaml)
//   - "name" field (optional for css): display name; inferred from filename if omitted
func (s *Server) adminUploadTheme(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxThemeUploadSize)
	if err := c.Request.ParseMultipartForm(MaxThemeUploadSize); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件过大或请求格式错误（上限 2MB）。"})
		return
	}

	var installed *themes.Theme
	var installErr error

	if zipFH, err := c.FormFile("zip"); err == nil && zipFH != nil {
		if zipFH.Size > MaxThemeUploadSize {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "ZIP 文件过大（上限 2MB）。"})
			return
		}
		f, err := zipFH.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取上传文件失败。"})
			return
		}
		defer f.Close()
		zipBytes, err := io.ReadAll(f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取上传文件失败。"})
			return
		}
		installed, installErr = themes.InstallFromZip(s.DataDir, zipBytes)
	} else if cssFH, err := c.FormFile("css"); err == nil && cssFH != nil {
		if cssFH.Size > MaxThemeUploadSize {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "CSS 文件过大（上限 2MB）。"})
			return
		}
		f, err := cssFH.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取上传文件失败。"})
			return
		}
		defer f.Close()
		cssBytes, err := io.ReadAll(f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取上传文件失败。"})
			return
		}
		displayName := c.PostForm("name")
		if displayName == "" {
			displayName = cssFH.Filename
		}
		installed, installErr = themes.InstallFromCSS(s.DataDir, displayName, cssBytes)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供 zip 或 css 文件。"})
		return
	}

	if installErr != nil {
		slog.Error("adminUploadTheme", "err", installErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": installErr.Error()})
		return
	}

	slog.Info("theme installed", "name", installed.Name, "builtin", installed.Builtin)
	c.JSON(http.StatusCreated, installed)
}

// DELETE /api/admin/themes/:name — delete a user-uploaded theme.
// Built-in themes cannot be deleted. If the deleted theme was the active
// style_theme, resets to "sunset".
func (s *Server) adminDeleteTheme(c *gin.Context) {
	name := c.Param("name")
	if !themes.ValidateName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的主题名。"})
		return
	}
	if err := themes.Delete(s.DataDir, name); err != nil {
		slog.Error("adminDeleteTheme", "name", name, "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := settings.Load(s.DataDir)
	if err == nil {
		if appearance, ok := cfg["appearance"].(map[string]interface{}); ok {
			if cur, _ := appearance["style_theme"].(string); cur == name {
				appearance["style_theme"] = "sunset"
				_ = settings.Save(s.DataDir, cfg)
			}
		}
	}
	slog.Info("theme deleted", "name", name)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// PUT /api/admin/themes/:name/activate — set a theme as the active style_theme.
func (s *Server) adminActivateTheme(c *gin.Context) {
	name := c.Param("name")
	if !themes.ValidateName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的主题名。"})
		return
	}
	t, err := themes.Get(s.DataDir, name)
	if err != nil || t == nil || !t.HasCSS {
		c.JSON(http.StatusNotFound, gin.H{"error": "主题不存在或无 CSS。"})
		return
	}
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取设置失败。"})
		return
	}
	appearance, ok := cfg["appearance"].(map[string]interface{})
	if !ok {
		appearance = map[string]interface{}{}
		cfg["appearance"] = appearance
	}
	appearance["style_theme"] = name
	if err := settings.Save(s.DataDir, cfg); err != nil {
		slog.Error("adminActivateTheme: save", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存设置失败。"})
		return
	}
	// Fire-and-forget revalidate so the EdgeOne / Cloudflare edge drops
	// the cached /api/site response (cache tag "site") and any HTML
	// that embedded the old style_theme. Without this the 3600s ISR
	// window wins and a refresh keeps showing the previous theme —
	// matching updateSettings (admin.go) which fires the same pair on
	// general settings saves. Pair with the "site" cache tag added in
	// web/lib/api/site.ts.
	s.revalidateFrontend([]string{"site", "home"}, []string{"/"})
	slog.Info("theme activated", "name", name)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "active": name})
}
