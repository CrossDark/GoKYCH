package api

import (
	"log/slog"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
	"gokych/internal/typst"
)

// safeFilenameRe keeps only ASCII letters, digits, dash, underscore, dot.
// Used to sanitize the article slug before plugging it into
// Content-Disposition. The slug already passes the article-create regex
// (`^[a-zA-Z0-9_-]+$`) so this is a defense-in-depth re-check.
var safeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// GET /api/articles/{type}/{slug}/pdf
//
// Typst-only: returns the compiled PDF for download. 404s cleanly for any
// other article type or for typst articles that fail to compile (e.g.
// typst CLI missing on the host, syntax error in source). The PDF is
// cached in typst_cache.pdf_content on first request; subsequent
// requests hit the cache and return instantly.
func (s *Server) getArticlePDF(c *gin.Context) {
	atype := c.Param("type")
	slug := c.Param("slug")
	if !parsers.IsValidType(atype) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文章类型。"})
		return
	}
	if atype != "typst" {
		// Be loud about the misroute — /api/articles/md/foo/pdf shouldn't
		// silently 200 with an empty PDF.
		c.JSON(http.StatusNotFound, gin.H{"error": "仅 typst 文章支持 PDF 下载。"})
		return
	}
	if !typst.Available() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "typst CLI 未安装。"})
		return
	}
	a, err := content.GetArticle(s.DB, atype, slug)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文章不存在。"})
		return
	}
	pdf, err := typst.CompilePDFCached(a.ID, a.Content)
	if err != nil {
		slog.Error("getArticlePDF: compile", "article_id", a.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PDF 编译失败。"})
		return
	}
	if len(pdf) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "PDF 生成失败（可能是源文件语法错误）。"})
		return
	}
	// Sanitize slug for Content-Disposition — should already be safe but we
	// don't want a future slug rule change to leak header-injection
	// characters (CR/LF/quote) into the response.
	filename := safeFilenameRe.ReplaceAllString(slug, "_")
	if filename == "" || filename == "_" {
		filename = "article"
	}
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`.pdf"`)
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, "application/pdf", pdf)
}
