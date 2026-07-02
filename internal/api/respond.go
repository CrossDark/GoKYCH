package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// respondErr writes a JSON error response with the given status and message.
// Mirrors the dominant `c.JSON(http.StatusX, gin.H{"error": msg})` pattern so
// handler bodies can stay one line and error wording stays consistent.
func respondErr(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"error": msg})
}

// respondInternalErr is the shortcut for the most common case — an unexpected
// server-side failure surfaced to the client with a generic Chinese message.
// Pass the underlying error for logging via slog if the caller already logs;
// otherwise pass nil and this helper will log once.
func respondInternalErr(c *gin.Context, msg string) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
}

// respondBadRequest is a typed shortcut for 400.
func respondBadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

// respondNotFound is a typed shortcut for 404.
func respondNotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"error": msg})
}

// errInvalidArticleType is the sentinel returned by articleIDFromParams when
// the {type} path param doesn't match any known article type. Callers should
// translate this into HTTP 400 (vs the 404 returned for missing articles).
var errInvalidArticleType = errors.New("invalid article type")

// requireArticleID resolves the article ID from the {type}/{slug} path params
// and writes the appropriate JSON error response on failure — callers only
// need to check the bool. The function returns false (and has already written
// the response) if the type is invalid (400) or the article is missing (404).
func (s *Server) requireArticleID(c *gin.Context) (int, bool) {
	id, err := s.articleIDFromParams(c)
	if err != nil {
		if err == errInvalidArticleType {
			respondBadRequest(c, "无效的文章类型。")
		} else {
			respondNotFound(c, "文章不存在。")
		}
		return 0, false
	}
	return id, true
}

// bindJSON decodes the request body into dst and writes a 400 "请求格式错误。"
// on failure, returning whether the body was successfully bound. dst must be a
// pointer.
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		respondBadRequest(c, "请求格式错误。")
		return false
	}
	return true
}

// validateLen trims s, returns "" when empty (writing a 400 "msg" if blank),
// and checks rune length against max (writing a 400 with a templated message
// when too long). label is the human-readable field name used in the
// over-limit message — e.g. "评论内容". Returns the trimmed string and ok.
func validateLen(c *gin.Context, s, label string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		respondBadRequest(c, label+"不能为空。")
		return "", false
	}
	if len([]rune(s)) > max {
		respondBadRequest(c, fmt.Sprintf("%s不能超过 %d 个字符。", label, max))
		return "", false
	}
	return s, true
}
