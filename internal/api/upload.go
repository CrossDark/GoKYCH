package api

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// maxUploadBytes caps a single upload at 10 MiB. Site static assets rarely
// exceed this; anything larger should go through a dedicated asset pipeline.
const maxUploadBytes = 10 << 20

// uploadFile handles POST /api/admin/files — accepts a multipart upload and
// stores it under <DataDir>/uploads/, recording a static_files row. The
// generated filename is random (16 bytes hex) with the original extension
// preserved, so two same-named uploads don't collide and the on-disk name
// can't be guessed to enumerate others.
func (s *Server) uploadFile(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未收到文件。"})
		return
	}
	if fh.Size > maxUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件过大，上限 10 MiB。"})
		return
	}

	src, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败。"})
		return
	}
	defer src.Close()

	// Sniff MIME from the declared header + filename extension. MIME sniffing
	// from raw bytes would need reading into a buffer and re-seeking; the
	// multipart header already carries a content type, so trust-and-trim it.
	mimeType := fh.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(fh.Filename))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Random on-disk name: <16 hex>.<ext>. Avoids collisions AND enumeration.
	randBuf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, randBuf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成文件名失败。"})
		return
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	storedName := hex.EncodeToString(randBuf) + ext
	uploadDir := filepath.Join(s.DataDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建上传目录失败。"})
		return
	}
	dstPath := filepath.Join(uploadDir, storedName)

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败。"})
		return
	}
	written, err := io.Copy(dst, src)
	dst.Close()
	if err != nil {
		os.Remove(dstPath) // cleanup partial
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入文件失败。"})
		return
	}

	// uploaded_by: optional, set when logged in (admin routes require it
	// anyway, but guard for nil).
	var uploaderID any
	if u := CurrentUserFromContext(c); u != nil {
		uploaderID = u.ID
	}

	res, err := s.DB.Exec(
		`INSERT INTO static_files (filename, original_name, file_path, file_size, mime_type, uploaded_by)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		storedName, fh.Filename, dstPath, written, mimeType, uploaderID,
	)
	if err != nil {
		os.Remove(dstPath) // rollback file so the table stays the source of truth
		c.JSON(http.StatusInternalServerError, gin.H{"error": "记录文件元数据失败。"})
		return
	}
	id, _ := res.LastInsertId()

	c.JSON(http.StatusCreated, gin.H{
		"id":            id,
		"filename":      storedName,
		"original_name": fh.Filename,
		"file_size":     written,
		"mime_type":     mimeType,
		"url":           "/uploads/" + storedName,
		"created_at":    time.Now(),
	})
}
