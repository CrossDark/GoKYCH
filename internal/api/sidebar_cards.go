package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ── Sidebar cards ─────────────────────────────────────────────────────
//
// Two surfaces against the same `sidebar_cards` table:
//
//   • Public read  — /api/sidebar-cards
//       The site-header ☰ icon (left of the screen name) opens a
//       drawer; its cards come from this endpoint. Only `is_active=1`
//       rows, ordered by `(sort_order, id)`. Cached aggressively because
//       the Set is the same for every visitor; the admin write paths
//       invalidate via revalidateFrontend so users see fresh content
//       within seconds of an edit.
//
//   • Admin CRUD    — /api/admin/sidebar-cards[/:id]
//       requireAdmin — site admins (and the owner role) manage the
//       card list. Public read endpoint already hides is_active=0 rows
//       so soft-deletes are common; full DELETE is also supported.
//
// Sorting model: `sort_order` is admin-controlled, low = top. Within
// the same sort_order value newer IDs win so two cards never collide
// on the visible order.

type sidebarCard struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	IsExternal  bool   `json:"is_external"`
	IsActive    bool   `json:"is_active"`
}

// cardInput is the wire shape for POST / PUT. Same fields across both —
// PUT partial updates weren't requested (the admin table has full row
// edit) and the simpler API surface keeps the JS call sites identical
// between create and edit.
//
// IsExternal / IsActive are pointers because the lack of a JSON field
// must NOT mean "false" — a card that's newly created without an
// explicit is_active should default to active (visible) so authors
// don't accidentally publish an invisible row. The pointer tri-state:
//   nil  → unset, treat as the safe default (active=true, external=false)
//   false → admin explicitly opted out
//   true  → admin set it
type cardInput struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	IsExternal  *bool  `json:"is_external"`
	IsActive    *bool  `json:"is_active"`
}

const (
	sidebarCardTitleMaxLen  = 64
	sidebarCardURLMaxLen    = 512
	sidebarCardIconMaxLen   = 32
	sidebarCardDescMaxLen   = 256
)

// GET /api/sidebar-cards
//
// Public; returns the active card list in drawer order. Anonymous
// callers are served from the CDN/edge cache (see Cache-Control set
// below) so this endpoint barely touches the origin once warm.
func (s *Server) listSidebarCards(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, title, url, icon, description, sort_order, is_external, is_active
		FROM sidebar_cards
		WHERE is_active = 1
		ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		slog.Error("listSidebarCards", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载侧栏卡片失败。"})
		return
	}
	defer rows.Close()
	out := make([]sidebarCard, 0)
	for rows.Next() {
		var card sidebarCard
		var ext, act int
		if err := rows.Scan(&card.ID, &card.Title, &card.URL, &card.Icon, &card.Description,
			&card.SortOrder, &ext, &act); err != nil {
			continue
		}
		card.IsExternal = ext == 1
		card.IsActive = act == 1
		out = append(out, card)
	}
	if err := rows.Err(); err != nil {
		slog.Error("listSidebarCards: iterate", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载侧栏卡片失败。"})
		return
	}
	// 5 min fresh + 1 hr stale-while-revalidate — the same content for
	// every visitor, and admin mutations call revalidateFrontend to
	// push fresh content within seconds of an edit. Emits a Vary so
	// any future locale-aware split doesn't cross-contaminate.
	c.Header("Cache-Control", "public, max-age=300, stale-while-revalidate=3600")
	c.JSON(http.StatusOK, out)
}

// GET /api/admin/sidebar-cards
//
// requireAdmin. Lists every row (including is_active=0) so the admin
// table can both reorder and un-hide without re-creating.
func (s *Server) listAdminSidebarCards(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, title, url, icon, description, sort_order, is_external, is_active
		FROM sidebar_cards
		ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		slog.Error("listAdminSidebarCards", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载侧栏卡片失败。"})
		return
	}
	defer rows.Close()
	out := make([]sidebarCard, 0)
	for rows.Next() {
		var card sidebarCard
		var ext, act int
		if err := rows.Scan(&card.ID, &card.Title, &card.URL, &card.Icon, &card.Description,
			&card.SortOrder, &ext, &act); err != nil {
			continue
		}
		card.IsExternal = ext == 1
		card.IsActive = act == 1
		out = append(out, card)
	}
	if err := rows.Err(); err != nil {
		slog.Error("listAdminSidebarCards: iterate", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载侧栏卡片失败。"})
		return
	}
	c.JSON(http.StatusOK, out)
}

// POST /api/admin/sidebar-cards — create
//
// requireAdmin. Validates title / url / icon / description length and
// rejects clearly-malformed URLs (`javascript:`, schema-less, etc.).
// `sort_order` defaults to (max+1) when omitted so appending a new
// card doesn't require the admin to think about ordering.
func (s *Server) createSidebarCard(c *gin.Context) {
	var in cardInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.URL = strings.TrimSpace(in.URL)
	in.Icon = strings.TrimSpace(in.Icon)
	in.Description = strings.TrimSpace(in.Description)

	if in.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空。"})
		return
	}
	if r := []rune(in.Title); len(r) > sidebarCardTitleMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题长度不能超过 " + strconv.Itoa(sidebarCardTitleMaxLen) + " 个字符。"})
		return
	}
	if r := []rune(in.URL); len(r) > sidebarCardURLMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "链接长度不能超过 " + strconv.Itoa(sidebarCardURLMaxLen) + " 个字符。"})
		return
	}
	if !looksLikeReasonableURL(in.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "链接格式无效（需以 http:// 或 https://、/ 或锚点开头）。"})
		return
	}
	if r := []rune(in.Icon); len(r) > sidebarCardIconMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图标长度不能超过 " + strconv.Itoa(sidebarCardIconMaxLen) + " 个字符。"})
		return
	}
	if r := []rune(in.Description); len(r) > sidebarCardDescMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "描述长度不能超过 " + strconv.Itoa(sidebarCardDescMaxLen) + " 个字符。"})
		return
	}

	ctx := c.Request.Context()
	ext := 0
	if in.IsExternal != nil && *in.IsExternal {
		ext = 1
	}
	act := 1
	if in.IsActive != nil && !*in.IsActive {
		act = 0
	}

	// If sort_order wasn't supplied (== 0) append to the bottom so the
	// admin doesn't have to set it manually on every create.
	sortOrder := in.SortOrder
	if sortOrder == 0 {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 100 FROM sidebar_cards`).Scan(&sortOrder)
	}

	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO sidebar_cards (title, url, icon, description, sort_order, is_external, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.Title, in.URL, in.Icon, in.Description, sortOrder, ext, act)
	if err != nil {
		slog.Error("createSidebarCard", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建侧栏卡片失败。"})
		return
	}
	id, _ := res.LastInsertId()
	// Bust the edge cache for the public read endpoint — every page on
	// the site embeds the drawer, so we don't pin a specific path.
	// `home` is the closest existing tag; add nothing narrower here.
	s.revalidateFrontend([]string{"home", "sidebar_cards"}, nil)
	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "ok"})
}

// PUT /api/admin/sidebar-cards/:id — update
func (s *Server) updateSidebarCard(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的侧栏卡片 ID。"})
		return
	}
	var in cardInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.URL = strings.TrimSpace(in.URL)
	in.Icon = strings.TrimSpace(in.Icon)
	in.Description = strings.TrimSpace(in.Description)

	if in.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空。"})
		return
	}
	if r := []rune(in.Title); len(r) > sidebarCardTitleMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题长度不能超过 " + strconv.Itoa(sidebarCardTitleMaxLen) + " 个字符。"})
		return
	}
	if r := []rune(in.URL); len(r) > sidebarCardURLMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "链接长度不能超过 " + strconv.Itoa(sidebarCardURLMaxLen) + " 个字符。"})
		return
	}
	if !looksLikeReasonableURL(in.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "链接格式无效（需以 http:// 或 https://、/ 或锚点开头）。"})
		return
	}
	if r := []rune(in.Icon); len(r) > sidebarCardIconMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图标长度不能超过 " + strconv.Itoa(sidebarCardIconMaxLen) + " 个字符。"})
		return
	}
	if r := []rune(in.Description); len(r) > sidebarCardDescMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "描述长度不能超过 " + strconv.Itoa(sidebarCardDescMaxLen) + " 个字符。"})
		return
	}
	ext := 0
	if in.IsExternal != nil && *in.IsExternal {
		ext = 1
	}
	act := 0
	if in.IsActive == nil || *in.IsActive {
		act = 1
	}

	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx, `
		UPDATE sidebar_cards
		SET title=?, url=?, icon=?, description=?, sort_order=?, is_external=?, is_active=?
		WHERE id=?`,
		in.Title, in.URL, in.Icon, in.Description, in.SortOrder, ext, act, id)
	if err != nil {
		slog.Error("updateSidebarCard", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新侧栏卡片失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "侧栏卡片不存在。"})
		return
	}
	s.revalidateFrontend([]string{"home", "sidebar_cards"}, nil)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DELETE /api/admin/sidebar-cards/:id — hard delete
//
// Soft delete (is_active=0) is the normal hide path but admin tools
// sometimes want to fully remove an entry; keep both available. The
// handler is one line either way.
func (s *Server) deleteSidebarCard(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的侧栏卡片 ID。"})
		return
	}
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sidebar_cards WHERE id=?`, id)
	if err != nil {
		slog.Error("deleteSidebarCard", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除侧栏卡片失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "侧栏卡片不存在。"})
		return
	}
	s.revalidateFrontend([]string{"home", "sidebar_cards"}, nil)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// looksLikeReasonableURL guards against `javascript:`, `data:`, and
// other injected-scheme attacks. Allows:
//   - absolute http(s)        — external destinations
//   - scheme-relative //foo   — protocol-relative (rare but legit)
//   - /path                   — internal site links (preferred for our own pages)
//   - #anchor                 — in-page anchors
//   - mailto:                 — admin cards can link to an email address
// Anything else (javascript:, data:, vbscript:, whitespace-only, …)
// is rejected so the admin form can't be turned into XSS by an attacker
// who somehow gets a write into the table.
func looksLikeReasonableURL(raw string) bool {
	if raw == "" {
		return false
	}
	// Disallow CR / LF / tab / control chars in URLs outright.
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "#") ||
		strings.HasPrefix(raw, "//") {
		return true
	}
	if u, err := url.Parse(raw); err == nil {
		scheme := strings.ToLower(u.Scheme)
		if scheme == "http" || scheme == "https" || scheme == "mailto" {
			return u.Host != "" || u.Opaque != "" || scheme == "mailto"
		}
	}
	return false
}
