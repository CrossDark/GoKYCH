package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// MaxUploadSize caps a single multipart upload at 10 MB. Anything larger is
// rejected at the parse-multipart stage before we read it into memory.
const MaxUploadSize = 10 * 1024 * 1024

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
	if _, statErr := os.Stat(filePath); statErr != nil {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "文件读取失败。"})
			return
		}
		out, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			slog.Error("uploadFile: open dst", "path", filePath, "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败。"})
			return
		}
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

	var uploadedBy *int
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		uploadedBy = &uid
	}
	_, err = s.DB.Exec(
		`INSERT INTO static_files (filename, original_name, file_path, file_size, mime_type, uploaded_by)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		filename, fileHeader.Filename, filePath, fileHeader.Size, detected, uploadedBy)
	if err != nil {
		// Likely a UNIQUE-collision on filename (duplicate upload); surface the
		// existing record instead of failing the request.
		if isDuplicateEntry(err) {
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
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文件 ID。"})
		return
	}
	var filename, filePath string
	err = s.DB.QueryRow(`SELECT filename, file_path FROM static_files WHERE id = ?`, id).Scan(&filename, &filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在。"})
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM static_files WHERE id = ?`, id); err != nil {
		slog.Error("deleteFile: db", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除文件记录失败。"})
		return
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		// DB record is gone; surface the FS failure but don't roll back — the
		// next cleanup pass (out of scope here) will catch orphans.
		slog.Warn("deleteFile: os.Remove failed", "id", id, "path", filePath, "err", err)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
