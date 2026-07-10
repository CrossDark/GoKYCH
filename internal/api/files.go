package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	authuser "gokych/internal/auth/user"
	coredb "gokych/internal/core/db"
	"gokych/internal/core/settings"
)

// MaxUploadSize caps a single multipart upload at 10 MB. Anything larger is
// rejected at the parse-multipart stage before we read it into memory.
const MaxUploadSize = 10 * 1024 * 1024

const featureAllowUserFileManagement = "allow_user_file_management"

func (s *Server) regularUserFileManagementAllowed() (bool, error) {
	cfg, err := settings.Load(s.DataDir)
	if err != nil {
		return false, err
	}
	features, _ := cfg["features"].(map[string]interface{})
	allow, _ := features[featureAllowUserFileManagement].(bool)
	return allow, nil
}

// resolveFileAccess returns the caller and whether they can see/manage every
// static file. Admins and owners always can. Regular users are admitted only
// when the owner has enabled features.allow_user_file_management, and handlers
// must then scope reads/deletes to uploaded_by = caller.ID.
func (s *Server) resolveFileAccess(c *gin.Context) (*authuser.User, bool, bool) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return nil, false, false
	}
	if authuser.IsAdmin(u.Role) {
		return u, true, true
	}
	allow, err := s.regularUserFileManagementAllowed()
	if err != nil {
		slog.Error("resolveFileAccess: settings.Load", "err", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "读取文件权限设置失败。"})
		return nil, false, false
	}
	if !allow {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "文件上传未向普通用户开放。"})
		return nil, false, false
	}
	return u, false, true
}

// allowedUploadMIMEs is the server-side allowlist of content types. The MIME
// is detected from the file header (http.DetectContentType) rather than
// trusting the client-supplied Content-Type, so a malicious admin uploading
// "image/jpeg" with a polyglot payload still gets rejected.
var allowedUploadMIMEs = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"image/svg+xml":   true,
	"image/x-icon":    true,
	"text/plain":      true,
	"text/markdown":   true,
	"text/css":        true,
	"application/pdf": true,
}

// uploadDir is the on-disk destination for uploads.
func (s *Server) uploadDir() string {
	return filepath.Join(s.DataDir, "uploads")
}

// publicAssetURL turns a server-relative path like "/uploads/abc.jpg"
// into an absolute URL by prepending PublicURL. When PublicURL is
// empty (dev) the path is returned unchanged — the Next.js dev
// server's /uploads/* rewrite covers it there. In production the
// frontend lives on a different origin (CF Pages), so the browser
// can't resolve a relative /uploads/* against its own host; the
// absolute form points it straight at the API.
func (s *Server) publicAssetURL(relPath string) string {
	if s.PublicURL == "" {
		return relPath
	}
	// Trim a single trailing slash on PublicURL so we don't end up
	// with "https://api.example.com//uploads/..." (which some
	// strict parsers / CDNs reject).
	base := strings.TrimRight(s.PublicURL, "/")
	// relPath is always already absolute-style (starts with "/"),
	// so we just concatenate.
	return base + relPath
}

// staticAssetPathRe matches site-relative asset paths in HTML attribute
// values. It matches both src="/uploads/..." and href="/uploads/..."
// (and /avatars/...) so that rendered wikidot/markdown/bbcode images
// and anchor links to uploads resolve correctly when the frontend is
// hosted on a different origin (e.g. EdgeOne Pages at eo.kych.net vs
// the Go API at api.kych.net).
//
// The regex is conservative: it requires the attribute to be followed
// by =" and a leading slash, and only rewrites paths that start with
// /uploads/ or /avatars/ — other same-origin links (e.g. /wikidot/foo)
// are left alone because they are routes handled by the frontend.
var staticAssetPathRe = regexp.MustCompile(`(src|href)="/(uploads|avatars)/([^"]+)"`)

// rewriteStaticAssetURLs rewrites site-relative /uploads/... and
// /avatars/... paths in HTML to absolute URLs using PublicURL. This is
// needed in cross-origin deployments (frontend on CDN, backend on a
// separate host) because a relative <img src="/uploads/foo.png"> in
// rendered article HTML would resolve against the CDN origin, which
// doesn't serve those files and returns a 504/404. When PublicURL is
// empty (dev) the HTML is returned unchanged.
func (s *Server) rewriteStaticAssetURLs(html string) string {
	if s.PublicURL == "" {
		return html
	}
	base := strings.TrimRight(s.PublicURL, "/")
	return staticAssetPathRe.ReplaceAllStringFunc(html, func(match string) string {
		// match looks like `src="/uploads/foo.png"` — rebuild it with
		// the base URL prepended to the path.
		sub := staticAssetPathRe.FindStringSubmatch(match)
		if len(sub) != 4 {
			return match
		}
		attr := sub[1]
		dir := sub[2]
		file := sub[3]
		return fmt.Sprintf(`%s="%s/%s/%s"`, attr, base, dir, file)
	})
}

// extFromMIME returns a sensible file extension for a MIME type, falling back
// to the standard library lookup so uncommon types still get something usable.
func extFromMIME(m string) string {
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/x-icon":
		return ".ico"
	case "text/plain":
		return ".txt"
	case "text/markdown":
		return ".md"
	case "text/css":
		return ".css"
	case "application/pdf":
		return ".pdf"
	}
	if exts, _ := mime.ExtensionsByType(m); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// POST /api/admin/files — multipart upload of a single file (form field: file).
func (s *Server) uploadFile(c *gin.Context) {
	ctx := c.Request.Context()
	u, _, ok := s.resolveFileAccess(c)
	if !ok {
		return
	}
	if err := c.Request.ParseMultipartForm(MaxUploadSize); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件过大或请求格式错误（上限 10MB）。"})
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未提供文件。"})
		return
	}
	if fileHeader.Size > MaxUploadSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件过大（上限 10MB）。"})
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		slog.Error("uploadFile: open", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件读取失败。"})
		return
	}
	defer file.Close()

	// Sniff the first 512 bytes to detect the real MIME — never trust the
	// client-supplied Content-Type.
	buf := make([]byte, 512)
	n, _ := io.ReadFull(file, buf)
	detected := http.DetectContentType(buf[:n])
	// http.DetectContentType returns "text/plain; charset=utf-8" for plain text;
	// strip the charset suffix before matching against the allowlist.
	if i := strings.IndexByte(detected, ';'); i >= 0 {
		detected = strings.TrimSpace(detected[:i])
	}
	if !allowedUploadMIMEs[detected] {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("不支持的文件类型: %s", detected)})
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件读取失败。"})
		return
	}

	// Hash the contents → opaque storage filename. Avoids leaking the original
	// name, prevents path traversal in the storage layer, and dedupes identical
	// uploads (the DB insert will then collide on UNIQUE filename and we keep
	// the first one rather than duplicating).
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		slog.Error("uploadFile: hash", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件读取失败。"})
		return
	}
	sum := hasher.Sum(nil)
	ext := filepath.Ext(fileHeader.Filename)
	if ext == "" {
		ext = extFromMIME(detected)
	}
	filename := hex.EncodeToString(sum)[:24] + ext

	if err := os.MkdirAll(s.uploadDir(), 0o755); err != nil {
		slog.Error("uploadFile: mkdir", "dir", s.uploadDir(), "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器存储错误。"})
		return
	}
	filePath := filepath.Join(s.uploadDir(), filename)

	// Only write if not already present — keeps the storage deterministic for
	// duplicate uploads (and lets the DB UNIQUE catch concurrent writes).
	// O_EXCL atomically fails if the file already exists, eliminating the
	// TOCTOU race between the old Stat + OpenFile pattern.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件读取失败。"})
		return
	}
	out, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// File already exists (concurrent duplicate upload) — skip write,
			// the DB UNIQUE constraint will catch the duplicate below.
		} else {
			slog.Error("uploadFile: open dst", "path", filePath, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败。"})
			return
		}
	} else {
		if _, err := io.Copy(out, file); err != nil {
			_ = out.Close()
			slog.Error("uploadFile: write", "path", filePath, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败。"})
			return
		}
		if err := out.Close(); err != nil {
			slog.Error("uploadFile: close", "path", filePath, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败。"})
			return
		}
	}

	uploadedBy := &u.ID
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO static_files (filename, original_name, file_path, file_size, mime_type, uploaded_by)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		filename, fileHeader.Filename, filePath, fileHeader.Size, detected, uploadedBy)
	if err != nil {
		// Likely a UNIQUE-collision on filename (duplicate upload); surface the
		// existing record instead of failing the request.
		if coredb.IsDuplicateEntry(err) {
			c.JSON(http.StatusOK, gin.H{
				"status":   "ok",
				"filename": filename,
				"url":      s.publicAssetURL("/uploads/" + filename),
				"deduped":  true,
			})
			return
		}
		slog.Error("uploadFile: db insert", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "记录文件失败。"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"status":   "ok",
		"filename": filename,
		"url":      s.publicAssetURL("/uploads/" + filename),
	})
}

// DELETE /api/admin/files/:id — remove the file from disk and the static_files
// table. DB delete first so we never have a dangling record pointing at a file
// we still need to inspect; filesystem delete is best-effort and logs on error.
func (s *Server) deleteFile(c *gin.Context) {
	ctx := c.Request.Context()
	u, canManageAll, ok := s.resolveFileAccess(c)
	if !ok {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文件 ID。"})
		return
	}
	var filename, filePath string
	var uploadedBy sql.NullInt64
	err = s.DB.QueryRowContext(ctx, `SELECT filename, file_path, uploaded_by FROM static_files WHERE id = ?`, id).Scan(&filename, &filePath, &uploadedBy)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在。"})
		return
	}
	if !canManageAll && (!uploadedBy.Valid || int(uploadedBy.Int64) != u.ID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在或不属于你。"})
		return
	}
	var res sql.Result
	if canManageAll {
		res, err = s.DB.ExecContext(ctx, `DELETE FROM static_files WHERE id = ?`, id)
	} else {
		res, err = s.DB.ExecContext(ctx, `DELETE FROM static_files WHERE id = ? AND uploaded_by = ?`, id, u.ID)
	}
	if err != nil {
		slog.Error("deleteFile: db", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除文件记录失败。"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在或不属于你。"})
		return
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		// DB record is gone; surface the FS failure but don't roll back — the
		// next cleanup pass (out of scope here) will catch orphans.
		slog.Warn("deleteFile: os.Remove failed", "id", id, "path", filePath, "err", err)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
