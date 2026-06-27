package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/password"
	"gokych/internal/auth/user"
	"gokych/internal/content"
	"gokych/internal/content/parsers"
	"gokych/internal/core/settings"
)

// ─── Users ───────────────────────────────────────────────────────────

type userSummary struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// GET /api/admin/users
func (s *Server) listUsers(c *gin.Context) {
	rows, err := s.DB.Query(
		`SELECT id, username, nickname, role, created_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载用户失败。"})
		return
	}
	defer rows.Close()
	var users = make([]userSummary, 0)
	for rows.Next() {
		var u userSummary
		if err := rows.Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.CreatedAt); err != nil {
			continue
		}
		users = append(users, u)
	}
	c.JSON(http.StatusOK, users)
}

type createUserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
}

// POST /api/admin/users
func (s *Server) createUser(c *gin.Context) {
	var in createUserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	if msg := password.ValidateUsername(in.Username); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if msg := password.ValidateStrength(in.Password); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if !user.IsValidRole(in.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的角色。"})
		return
	}
	// admin 不得创建 owner 账户（防止垂直提权）。owner 仅由 seed 或现有 owner 提权产生。
	if in.Role == user.RoleOwner {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能创建 owner 账户。"})
		return
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败。"})
		return
	}
	id, err := user.Create(s.DB, in.Username, hash, in.Nickname, in.Role)
	if err != nil {
		if isDuplicateEntry(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败。"})
		return
	}
	u, _ := user.GetByID(s.DB, int(id))
	c.JSON(http.StatusCreated, u)
}

type updateRoleInput struct {
	Role string `json:"role"`
}

// PUT /api/admin/users/:username/role
func (s *Server) updateUserRole(c *gin.Context) {
	username := c.Param("username")
	var in updateRoleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	if !user.IsValidRole(in.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的角色。"})
		return
	}
	ok, err := user.UpdateInfo(s.DB, username, "", in.Role)
	if err != nil || !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DELETE /api/admin/users/:username
func (s *Server) deleteUser(c *gin.Context) {
	username := c.Param("username")
	if username == "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除默认管理员。"})
		return
	}
	ok, err := user.Delete(s.DB, username)
	if err != nil || !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── Tags ────────────────────────────────────────────────────────────

const tagNameMaxLen = 64 // schema: tags.name VARCHAR(64)

type tagInput struct {
	Name string `json:"name"`
}

// GET /api/admin/tags — full tag list with article counts, ordered by usage.
func (s *Server) listAdminTags(c *gin.Context) {
	rows, err := s.DB.Query(
		`SELECT t.id, t.name, COUNT(at.tag_id) AS cnt
		 FROM tags t LEFT JOIN article_tags at ON t.id = at.tag_id
		 GROUP BY t.id, t.name ORDER BY cnt DESC, t.name`)
	if err != nil {
		slog.Error("listAdminTags", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载标签失败。"})
		return
	}
	defer rows.Close()
	type tagRow struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := []tagRow{}
	for rows.Next() {
		var t tagRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err == nil {
			out = append(out, t)
		}
	}
	c.JSON(http.StatusOK, out)
}

// POST /api/admin/tags — create a tag. Idempotent: if the name already
// exists, returns the existing id with `existed: true` rather than 409, so
// the admin UI's "create new" button can never fail loudly.
func (s *Server) createTag(c *gin.Context) {
	var in tagInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标签名不能为空。"})
		return
	}
	if len([]rune(in.Name)) > tagNameMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("标签名不能超过 %d 个字符。", tagNameMaxLen)})
		return
	}
	id, err := content.GetOrCreateTag(s.DB, in.Name)
	if err != nil {
		// Race against a concurrent INSERT — refetch the existing row.
		var existing int
		if scanErr := s.DB.QueryRow(`SELECT id FROM tags WHERE name = ?`, in.Name).Scan(&existing); scanErr == nil {
			c.JSON(http.StatusOK, gin.H{"id": existing, "status": "ok", "existed": true})
			return
		}
		slog.Error("createTag", "name", in.Name, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建标签失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "ok"})
}

// PUT /api/admin/tags/:id — rename a tag (cascades to article_tags via
// the shared id, which is what we want — only the display name changes).
func (s *Server) renameTag(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的标签 ID。"})
		return
	}
	var in tagInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标签名不能为空。"})
		return
	}
	if len([]rune(in.Name)) > tagNameMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("标签名不能超过 %d 个字符。", tagNameMaxLen)})
		return
	}
	res, err := s.DB.Exec(`UPDATE tags SET name = ? WHERE id = ?`, in.Name, id)
	if err != nil {
		if isDuplicateEntry(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "已存在同名标签。"})
			return
		}
		slog.Error("renameTag", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重命名失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "标签不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DELETE /api/admin/tags/:id — drop the tag and unlink it from any articles.
// Wrapped in a transaction so an article_tags delete failure doesn't leave
// the tags row orphaned from its references.
func (s *Server) deleteTag(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的标签 ID。"})
		return
	}
	tx, err := s.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "事务启动失败。"})
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM article_tags WHERE tag_id = ?`, id); err != nil {
		slog.Error("deleteTag: clear article_tags", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理标签关联失败。"})
		return
	}
	res, err := tx.Exec(`DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		slog.Error("deleteTag", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "标签不存在。"})
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("deleteTag: commit", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "提交失败。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── Notifications ────────────────────────────────────────────────────

// GET /api/admin/notifications
func (s *Server) listAdminNotifications(c *gin.Context) {
	type notif struct {
		ID          int       `json:"id"`
		Title       string    `json:"title"`
		Content     string    `json:"content"`
		ContentHTML string    `json:"content_html"`
		IsImportant bool      `json:"is_important"`
		IsActive    bool      `json:"is_active"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	rows, err := s.DB.Query(
		`SELECT id, title, content, is_important, is_active, updated_at
		 FROM notifications ORDER BY updated_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载通知失败。"})
		return
	}
	defer rows.Close()
	var out = make([]notif, 0)
	for rows.Next() {
		var n notif
		var imp, act int
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &imp, &act, &n.UpdatedAt); err != nil {
			continue
		}
		n.IsImportant = imp == 1
		n.IsActive = act == 1
		n.ContentHTML = parsers.RenderSafeMarkdown(n.Content)
		out = append(out, n)
	}
	c.JSON(http.StatusOK, out)
}

type notifInput struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	IsImportant bool   `json:"is_important"`
	IsActive    bool   `json:"is_active"`
}

// Max lengths for notification fields, aligned with the schema column widths
// (title VARCHAR(255), content TEXT — capped to 2000 in the app layer to keep
// payloads manageable and prevent DB growth from malicious admins).
const (
	notificationTitleMaxLen   = 255
	notificationContentMaxLen = 2000
)

// POST /api/admin/notifications
func (s *Server) createNotification(c *gin.Context) {
	var in notifInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	if in.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空。"})
		return
	}
	if len([]rune(in.Title)) > notificationTitleMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("标题长度不能超过 %d 个字符。", notificationTitleMaxLen)})
		return
	}
	if len([]rune(in.Content)) > notificationContentMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("内容长度不能超过 %d 个字符。", notificationContentMaxLen)})
		return
	}
	imp := 0
	if in.IsImportant {
		imp = 1
	}
	act := 1 // always active on creation (toggle via update)
	res, err := s.DB.Exec(
		`INSERT INTO notifications (title, content, is_important, is_active) VALUES (?, ?, ?, ?)`,
		in.Title, in.Content, imp, act)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建通知失败。"})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "ok"})
}

// PUT /api/admin/notifications/:id
func (s *Server) updateNotification(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的通知 ID。"})
		return
	}
	var in notifInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	if in.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空。"})
		return
	}
	if len([]rune(in.Title)) > notificationTitleMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("标题长度不能超过 %d 个字符。", notificationTitleMaxLen)})
		return
	}
	if len([]rune(in.Content)) > notificationContentMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("内容长度不能超过 %d 个字符。", notificationContentMaxLen)})
		return
	}
	imp := 0
	if in.IsImportant {
		imp = 1
	}
	act := 0
	if in.IsActive {
		act = 1
	}
	res, err := s.DB.Exec(
		`UPDATE notifications SET title=?, content=?, is_important=?, is_active=? WHERE id=?`,
		in.Title, in.Content, imp, act, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新通知失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "通知不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DELETE /api/admin/notifications/:id
func (s *Server) deleteNotification(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的通知 ID。"})
		return
	}
	res, err := s.DB.Exec(`DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
		slog.Error("deleteNotification", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除通知失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "通知不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── Settings ─────────────────────────────────────────────────────────

// GET /api/admin/settings
func (s *Server) getSettings(c *gin.Context) {
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		slog.Error("getSettings: load", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解析设置文件失败。"})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// PUT /api/admin/settings
func (s *Server) updateSettings(c *gin.Context) {
	var cfg map[string]interface{}
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	if err := settings.Save(s.DataDir, cfg); err != nil {
		slog.Error("updateSettings: save", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存设置失败。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── Home Management ──────────────────────────────────────────────────

// GET /api/admin/home
func (s *Server) getAdminHome(c *gin.Context) {
	type link struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
		SortOrder   int    `json:"sort_order"`
	}
	links := []link{}
	rows, err := s.DB.Query(`SELECT id, name, url, description, sort_order FROM subsite_links ORDER BY sort_order`)
	if err != nil {
		slog.Error("getAdminHome: list subsite_links", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载子站点链接失败。"})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.ID, &l.Name, &l.URL, &l.Description, &l.SortOrder); err == nil {
			links = append(links, l)
		}
	}

	type featured struct {
		ID        int    `json:"id"`
		ArticleID int    `json:"article_id"`
		Title     string `json:"title"`
		Type      string `json:"type"`
		Slug      string `json:"slug"`
		SortOrder int    `json:"sort_order"`
	}
	feat := []featured{}
	rows2, err := s.DB.Query(
		`SELECT fa.id, fa.article_id, a.title, a.type, a.slug, fa.sort_order
		 FROM featured_articles fa JOIN articles a ON fa.article_id = a.id
		 ORDER BY fa.sort_order`)
	if err != nil {
		slog.Error("getAdminHome: list featured_articles", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载推荐文章失败。"})
		return
	}
	defer rows2.Close()
	for rows2.Next() {
		var f featured
		if err := rows2.Scan(&f.ID, &f.ArticleID, &f.Title, &f.Type, &f.Slug, &f.SortOrder); err == nil {
			feat = append(feat, f)
		}
	}
	c.JSON(http.StatusOK, gin.H{"subsite_links": links, "featured_articles": feat})
}

// Max lengths for subsite link fields, kept in sync with the schema's column
// widths. Returning 400 here gives a clearer error than letting MySQL truncate
// silently.
const (
	subsiteLinkNameMaxLen        = 255
	subsiteLinkDescriptionMaxLen = 500
)

// allowedSubsiteLinkSchemes is the URL scheme allowlist for nav links. Other
// schemes (e.g. javascript:, data:, vbscript:) would let an admin XSS visitors
// via the Header <a href={link.url}>. Path-only links ("/labels/...") are also
// accepted.
var allowedSubsiteLinkSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
}

// validateSubsiteLinkURL enforces the scheme allowlist and absolute-URL sanity.
// Returns "" when valid, or a user-facing error message.
func validateSubsiteLinkURL(raw string) string {
	if raw == "" {
		return "URL 不能为空。"
	}
	// Internal path-only links are allowed (must start with /, not //).
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "URL 必须以 http(s)://、mailto: 或 / 开头。"
	}
	if !allowedSubsiteLinkSchemes[strings.ToLower(u.Scheme)] {
		return "URL 协议不被允许（仅支持 http/https/mailto）。"
	}
	return ""
}

type linkInput struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
}

// POST /api/admin/home/links
func (s *Server) addSubsiteLink(c *gin.Context) {
	var in linkInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || in.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称和 URL 不能为空。"})
		return
	}
	if len([]rune(in.Name)) > subsiteLinkNameMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("名称长度不能超过 %d 个字符。", subsiteLinkNameMaxLen)})
		return
	}
	if len([]rune(in.Description)) > subsiteLinkDescriptionMaxLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("描述长度不能超过 %d 个字符。", subsiteLinkDescriptionMaxLen)})
		return
	}
	if msg := validateSubsiteLinkURL(in.URL); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	_, err := s.DB.Exec(
		`INSERT INTO subsite_links (name, url, description, sort_order) VALUES (?, ?, ?, ?)`,
		in.Name, in.URL, in.Description, in.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}

// DELETE /api/admin/home/links/:id
func (s *Server) deleteSubsiteLink(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的链接 ID。"})
		return
	}
	res, err := s.DB.Exec(`DELETE FROM subsite_links WHERE id = ?`, id)
	if err != nil {
		slog.Error("deleteSubsiteLink", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "链接不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type featuredInput struct {
	ArticleID int `json:"article_id"`
	SortOrder int `json:"sort_order"`
}

// POST /api/admin/home/featured
func (s *Server) addFeatured(c *gin.Context) {
	var in featuredInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	if in.ArticleID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章 ID。"})
		return
	}
	_, err := s.DB.Exec(
		`INSERT INTO featured_articles (article_id, sort_order) VALUES (?, ?)`,
		in.ArticleID, in.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}

// DELETE /api/admin/home/featured/:id
func (s *Server) deleteFeatured(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的推荐 ID。"})
		return
	}
	res, err := s.DB.Exec(`DELETE FROM featured_articles WHERE id = ?`, id)
	if err != nil {
		slog.Error("deleteFeatured", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "推荐不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ─── Files ────────────────────────────────────────────────────────────

// GET /api/admin/files
func (s *Server) listFiles(c *gin.Context) {
	type fileInfo struct {
		ID           int       `json:"id"`
		Filename     string    `json:"filename"`
		OriginalName string    `json:"original_name"`
		FileSize     int64     `json:"file_size"`
		MimeType     string    `json:"mime_type"`
		CreatedAt    time.Time `json:"created_at"`
		// URL is the absolute public URL of the file (publicAssetURL
		// prepends PublicURL when set; same as the field on the
		// upload response). Frontend renders this directly so it works
		// whether the API and frontend share an origin (dev) or not
		// (CF Pages + Ubuntu VM in prod).
		URL string `json:"url"`
	}
	rows, err := s.DB.Query(
		`SELECT id, filename, original_name, file_size, mime_type, created_at
		 FROM static_files ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文件列表失败。"})
		return
	}
	defer rows.Close()
	var out = make([]fileInfo, 0)
	for rows.Next() {
		var f fileInfo
		if err := rows.Scan(&f.ID, &f.Filename, &f.OriginalName, &f.FileSize, &f.MimeType, &f.CreatedAt); err != nil {
			continue
		}
		f.URL = s.publicAssetURL("/uploads/" + f.Filename)
		out = append(out, f)
	}
	c.JSON(http.StatusOK, out)
}

// ─── Profile ──────────────────────────────────────────────────────────

// GET /api/admin/profile
func (s *Server) getProfile(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	c.JSON(http.StatusOK, u)
}

type profileInput struct {
	Nickname     string `json:"nickname"`
	Bio          string `json:"bio"`
	Avatar       string `json:"avatar"`
	SocialEmail  string `json:"social_email"`
	SocialGithub string `json:"social_github"`
	SocialQQ     string `json:"social_qq"`
}

// PUT /api/admin/profile
func (s *Server) updateProfile(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	var in profileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	if err := user.UpdateProfile(s.DB, u.ID, in.Avatar, in.Bio,
		in.SocialEmail, in.SocialGithub, in.SocialQQ); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新资料失败。"})
		return
	}
	if in.Nickname != "" {
		user.UpdateInfo(s.DB, u.Username, in.Nickname, u.Role)
	}
	u2, _ := user.GetByID(s.DB, u.ID)
	c.JSON(http.StatusOK, u2)
}

type changePasswordInput struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// PUT /api/profile/password — self-service password change. Any authenticated
// user (user / admin / owner) may change their own password; they must prove
// knowledge of the current password first (defense against a shared-session
// attacker silently hijacking the account).
//
// Passkey note: if the caller is not the owner AND has at least one passkey
// registered, password login is normally disabled (see postLogin). That gate
// only blocks the *public* login path — the still-valid credential here is
// the existing session. We let users keep their password in sync even while
// passkey-first; should they later remove all passkeys, password login
// immediately works again with the new value.
func (s *Server) changeMyPassword(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	var in changePasswordInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	in.OldPassword = strings.TrimSpace(in.OldPassword)
	in.NewPassword = strings.TrimSpace(in.NewPassword)
	if in.OldPassword == "" || in.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "旧密码和新密码不能为空。"})
		return
	}
	if msg := password.ValidateStrength(in.NewPassword); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if in.OldPassword == in.NewPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码不能与旧密码相同。"})
		return
	}
	cur, err := user.GetWithPassword(s.DB, u.Username)
	if err != nil || cur == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载账号失败。"})
		return
	}
	if !password.Verify(cur.PasswordHash, in.OldPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "旧密码错误。"})
		return
	}
	hash, err := password.Hash(in.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败。"})
		return
	}
	if _, err := user.UpdatePassword(s.DB, u.Username, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新密码失败。"})
		return
	}
	slog.Info("user changed own password", "user_id", u.ID, "username", u.Username)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "密码已修改。"})
}

// ─── Helpers ──────────────────────────────────────────────────────────
//
// (defaultSettings used to live here as a stub with only three fields — the
// canonical version is now in internal/core/settings.Default() and is shared
// with /api/site and the on-disk settings.yml bootstrap.)
