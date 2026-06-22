package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"gokych/internal/auth/password"
	"gokych/internal/auth/user"
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
	hash, err := password.Hash(in.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败。"})
		return
	}
	id, err := user.Create(s.DB, in.Username, hash, in.Nickname, in.Role)
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate") {
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

// ─── Notifications ────────────────────────────────────────────────────

// GET /api/admin/notifications
func (s *Server) listAdminNotifications(c *gin.Context) {
	type notif struct {
		ID          int       `json:"id"`
		Title       string    `json:"title"`
		Content     string    `json:"content"`
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

// POST /api/admin/notifications
func (s *Server) createNotification(c *gin.Context) {
	var in notifInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
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
	id, _ := strconv.Atoi(c.Param("id"))
	var in notifInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
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
	_, err := s.DB.Exec(
		`UPDATE notifications SET title=?, content=?, is_important=?, is_active=? WHERE id=?`,
		in.Title, in.Content, imp, act, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新通知失败。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DELETE /api/admin/notifications/:id
func (s *Server) deleteNotification(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	res, err := s.DB.Exec(`DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
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
	path := s.DataDir + "/settings/settings.yml"
	data, err := os.ReadFile(path)
	if err != nil {
		// Return defaults.
		c.JSON(http.StatusOK, defaultSettings())
		return
	}
	var settings map[string]interface{}
	if err := yaml.Unmarshal(data, &settings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解析设置文件失败。"})
		return
	}
	c.JSON(http.StatusOK, settings)
}

// PUT /api/admin/settings
func (s *Server) updateSettings(c *gin.Context) {
	path := s.DataDir + "/settings/settings.yml"
	var settings map[string]interface{}
	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	out, err := yaml.Marshal(settings)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "序列化设置失败。"})
		return
	}
	if err := os.MkdirAll(s.DataDir+"/settings", 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建设置目录失败。"})
		return
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
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
	rows, _ := s.DB.Query(`SELECT id, name, url, description, sort_order FROM subsite_links ORDER BY sort_order`)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var l link
			if err := rows.Scan(&l.ID, &l.Name, &l.URL, &l.Description, &l.SortOrder); err == nil {
				links = append(links, l)
			}
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
	rows2, _ := s.DB.Query(
		`SELECT fa.id, fa.article_id, a.title, a.type, a.slug, fa.sort_order
		 FROM featured_articles fa JOIN articles a ON fa.article_id = a.id
		 ORDER BY fa.sort_order`)
	if rows2 != nil {
		defer rows2.Close()
		for rows2.Next() {
			var f featured
			if err := rows2.Scan(&f.ID, &f.ArticleID, &f.Title, &f.Type, &f.Slug, &f.SortOrder); err == nil {
				feat = append(feat, f)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"subsite_links": links, "featured_articles": feat})
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
	if in.Name == "" || in.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称和 URL 不能为空。"})
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
	id, _ := strconv.Atoi(c.Param("id"))
	_, _ = s.DB.Exec(`DELETE FROM subsite_links WHERE id = ?`, id)
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
	id, _ := strconv.Atoi(c.Param("id"))
	_, _ = s.DB.Exec(`DELETE FROM featured_articles WHERE id = ?`, id)
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
	Nickname string `json:"nickname"`
	Bio      string `json:"bio"`
	Avatar   string `json:"avatar"`
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
	if err := user.UpdateProfile(s.DB, u.ID, in.Avatar, in.Bio); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新资料失败。"})
		return
	}
	if in.Nickname != "" {
		user.UpdateInfo(s.DB, u.Username, in.Nickname, u.Role)
	}
	u2, _ := user.GetByID(s.DB, u.ID)
	c.JSON(http.StatusOK, u2)
}

// ─── Helpers ──────────────────────────────────────────────────────────

func defaultSettings() map[string]interface{} {
	return map[string]interface{}{
		"site": map[string]interface{}{
			"title":    "跨越晨昏",
			"subtitle": "个人网站",
			"language": "zh-CN",
		},
	}
}
