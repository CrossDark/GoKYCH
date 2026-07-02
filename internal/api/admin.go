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
	coredb "gokych/internal/core/db"
	"gokych/internal/core/settings"
)

type userSummary struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) listUsers(c *gin.Context) {
	ctx := c.Request.Context()
	users, err := user.ListCtx(ctx, s.DB)
	if err != nil {
		slog.Error("listUsers", "err", err)
		respondInternalErr(c, "加载用户失败。")
		return
	}
	out := make([]userSummary, 0, len(users))
	for _, u := range users {
		out = append(out, userSummary{
			ID:        u.ID,
			Username:  u.Username,
			Nickname:  u.Nickname,
			Role:      u.Role,
			CreatedAt: u.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

type createUserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
	Role     string `json:"role"`
}

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
	if in.Role == user.RoleOwner {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能创建 owner 账户。"})
		return
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败。"})
		return
	}
	ctx := c.Request.Context()
	id, err := user.CreateCtx(ctx, s.DB, in.Username, hash, in.Nickname, in.Role)
	if err != nil {
		if coredb.IsDuplicateEntry(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "用户名已存在。"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败。"})
		return
	}
	u, _ := user.GetByIDCtx(ctx, s.DB, int(id))
	c.JSON(http.StatusCreated, u)
}

type updateRoleInput struct {
	Role string `json:"role"`
}

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
	ctx := c.Request.Context()
	target, err := user.GetByUsernameCtx(ctx, s.DB, username)
	if err != nil || target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在。"})
		return
	}
	if user.IsOwner(target.Role) {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能更改 owner 的角色。"})
		return
	}
	ok, err := user.UpdateInfoCtx(ctx, s.DB, username, "", in.Role)
	if err != nil || !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) deleteUser(c *gin.Context) {
	username := c.Param("username")
	currentUser := CurrentUserFromContext(c)
	if currentUser != nil && currentUser.Username == username {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除自己的账号。"})
		return
	}
	ctx := c.Request.Context()
	target, err := user.GetByUsernameCtx(ctx, s.DB, username)
	if err != nil || target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在。"})
		return
	}
	if user.IsOwner(target.Role) {
		c.JSON(http.StatusForbidden, gin.H{"error": "不能删除所有者账号。"})
		return
	}
	ok, err := user.DeleteCtx(ctx, s.DB, username)
	if err != nil || !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

const tagNameMaxLen = 64

type tagInput struct {
	Name string `json:"name"`
}

func (s *Server) listAdminTags(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx,
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
	if err := rows.Err(); err != nil {
		slog.Error("listAdminTags: iterate rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载标签失败。"})
		return
	}
	c.JSON(http.StatusOK, out)
}

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
	ctx := c.Request.Context()
	id, err := content.GetOrCreateTagCtx(ctx, s.DB, in.Name)
	if err != nil {
		var existing int
		if scanErr := s.DB.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ?`, in.Name).Scan(&existing); scanErr == nil {
			c.JSON(http.StatusOK, gin.H{"id": existing, "status": "ok", "existed": true})
			return
		}
		slog.Error("createTag", "name", in.Name, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建标签失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "ok"})
}

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
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx, `UPDATE tags SET name = ? WHERE id = ?`, in.Name, id)
	if err != nil {
		if coredb.IsDuplicateEntry(err) {
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

func (s *Server) deleteTag(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的标签 ID。"})
		return
	}
	ctx := c.Request.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "事务启动失败。"})
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM article_tags WHERE tag_id = ?`, id); err != nil {
		slog.Error("deleteTag: clear article_tags", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清理标签关联失败。"})
		return
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
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
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx,
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
	if err := rows.Err(); err != nil {
		slog.Error("listAdminNotifications: iterate rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载通知失败。"})
		return
	}
	c.JSON(http.StatusOK, out)
}

type notifInput struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	IsImportant bool   `json:"is_important"`
	IsActive    bool   `json:"is_active"`
}

const (
	notificationTitleMaxLen   = 255
	notificationContentMaxLen = 2000
)

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
	act := 1
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO notifications (title, content, is_important, is_active) VALUES (?, ?, ?, ?)`,
		in.Title, in.Content, imp, act)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建通知失败。"})
		return
	}
	id, _ := res.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "ok"})
}

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
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx,
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

func (s *Server) deleteNotification(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的通知 ID。"})
		return
	}
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id)
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

func (s *Server) getSettings(c *gin.Context) {
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		slog.Error("getSettings: load", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解析设置文件失败。"})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

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

func (s *Server) getAdminHome(c *gin.Context) {
	type link struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
		SortOrder   int    `json:"sort_order"`
	}
	links := []link{}
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, url, description, sort_order FROM subsite_links ORDER BY sort_order`)
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
	if err := rows.Err(); err != nil {
		slog.Error("getAdminHome: iterate subsite_links rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载子站点链接失败。"})
		return
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
	rows2, err := s.DB.QueryContext(ctx,
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
	if err := rows2.Err(); err != nil {
		slog.Error("getAdminHome: iterate featured_articles rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载推荐文章失败。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"subsite_links": links, "featured_articles": feat})
}

const (
	subsiteLinkNameMaxLen        = 255
	subsiteLinkDescriptionMaxLen = 500
)

var allowedSubsiteLinkSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
}

func validateSubsiteLinkURL(raw string) string {
	if raw == "" {
		return "URL 不能为空。"
	}
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
	ctx := c.Request.Context()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO subsite_links (name, url, description, sort_order) VALUES (?, ?, ?, ?)`,
		in.Name, in.URL, in.Description, in.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}

func (s *Server) deleteSubsiteLink(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的链接 ID。"})
		return
	}
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx, `DELETE FROM subsite_links WHERE id = ?`, id)
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
	ctx := c.Request.Context()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO featured_articles (article_id, sort_order) VALUES (?, ?)`,
		in.ArticleID, in.SortOrder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "添加失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok"})
}

func (s *Server) deleteFeatured(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的推荐 ID。"})
		return
	}
	ctx := c.Request.Context()
	res, err := s.DB.ExecContext(ctx, `DELETE FROM featured_articles WHERE id = ?`, id)
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

func (s *Server) listFiles(c *gin.Context) {
	type fileInfo struct {
		ID           int       `json:"id"`
		Filename     string    `json:"filename"`
		OriginalName string    `json:"original_name"`
		FileSize     int64     `json:"file_size"`
		MimeType     string    `json:"mime_type"`
		CreatedAt    time.Time `json:"created_at"`
		URL          string    `json:"url"`
	}
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx,
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
	if err := rows.Err(); err != nil {
		slog.Error("listFiles: iterate rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载文件列表失败。"})
		return
	}
	c.JSON(http.StatusOK, out)
}

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
	ctx := c.Request.Context()
	if err := user.UpdateProfileCtx(ctx, s.DB, u.ID, in.Avatar, in.Bio,
		in.SocialEmail, in.SocialGithub, in.SocialQQ); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新资料失败。"})
		return
	}
	if in.Nickname != "" {
		_, _ = user.UpdateInfoCtx(ctx, s.DB, u.Username, in.Nickname, u.Role)
	}
	u2, _ := user.GetByIDCtx(ctx, s.DB, u.ID)
	c.JSON(http.StatusOK, u2)
}

type changePasswordInput struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

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
	ctx := c.Request.Context()
	cur, err := user.GetWithPasswordCtx(ctx, s.DB, u.Username)
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
	if _, err := user.UpdatePasswordCtx(ctx, s.DB, u.Username, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新密码失败。"})
		return
	}
	slog.Info("user changed own password", "user_id", u.ID, "username", u.Username)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "密码已修改。"})
}
