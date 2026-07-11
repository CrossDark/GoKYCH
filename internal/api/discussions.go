package api

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/user"
	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

func (s *Server) listDiscussions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	ctx := c.Request.Context()
	discussions, err := content.ListDiscussionsCtx(ctx, s.DB, page, 20)
	if err != nil {
		respondInternalErr(c, "加载讨论列表失败。")
		return
	}
	count, _ := content.CountDiscussionsCtx(ctx, s.DB)
	for i := range discussions {
		discussions[i].ContentHTML = s.renderDiscussionContent(discussions[i].Content, discussions[i].Format)
	}
	c.JSON(http.StatusOK, gin.H{
		"discussions": discussions,
		"total":       count,
		"page":        page,
	})
}

func (s *Server) getDiscussion(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		respondBadRequest(c, "无效的讨论链接。")
		return
	}
	ctx := c.Request.Context()
	d, err := content.GetDiscussionBySlugCtx(ctx, s.DB, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			c.Status(http.StatusNotFound)
		} else {
			respondInternalErr(c, "加载讨论失败。")
		}
		return
	}
	d.ContentHTML = s.renderDiscussionContent(d.Content, d.Format)

	replies, err := content.GetDiscussionRepliesCtx(ctx, s.DB, d.ID)
	if err != nil {
		respondInternalErr(c, "加载回复失败。")
		return
	}
	for i := range replies {
		replies[i].ContentHTML = s.renderDiscussionContent(replies[i].Content, replies[i].Format)
	}

	c.JSON(http.StatusOK, gin.H{
		"discussion": d,
		"replies":    replies,
	})
}

func (s *Server) createDiscussion(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	var in struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	if !bindJSON(c, &in) {
		return
	}
	in.Title, _ = validateLen(c, in.Title, "标题", 255)
	in.Content, _ = validateLen(c, in.Content, "内容", 65535)
	if in.Format != "md" && in.Format != "bbcode" && in.Format != "html" {
		in.Format = "md"
	}
	ctx := c.Request.Context()
	d, err := content.CreateDiscussionCtx(ctx, s.DB, in.Title, in.Content, in.Format, &u.ID)
	if err != nil {
		respondInternalErr(c, "创建讨论失败。")
		return
	}
	d.ContentHTML = s.renderDiscussionContent(d.Content, d.Format)
	c.JSON(http.StatusCreated, d)
}

func (s *Server) addDiscussionReply(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		respondBadRequest(c, "无效的讨论链接。")
		return
	}
	ctx := c.Request.Context()
	d, err := content.GetDiscussionBySlugCtx(ctx, s.DB, slug)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	var in struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	if !bindJSON(c, &in) {
		return
	}
	in.Content, _ = validateLen(c, in.Content, "回复内容", 65535)
	if in.Format != "md" && in.Format != "bbcode" && in.Format != "html" {
		in.Format = "md"
	}
	r, err := content.AddDiscussionReplyCtx(ctx, s.DB, d.ID, in.Content, in.Format, &u.ID, u.Username)
	if err != nil {
		respondInternalErr(c, "添加回复失败。")
		return
	}
	r.ContentHTML = s.renderDiscussionContent(r.Content, r.Format)
	c.JSON(http.StatusCreated, r)
}

func (s *Server) deleteDiscussion(c *gin.Context) {
	slug := c.Param("slug")
	if slug == "" {
		respondBadRequest(c, "无效的讨论链接。")
		return
	}
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	ctx := c.Request.Context()
	d, err := content.GetDiscussionBySlugCtx(ctx, s.DB, slug)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if d.AuthorID == nil || *d.AuthorID != u.ID {
		if !user.IsAdmin(u.Role) {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权限删除此讨论。"})
			return
		}
	}
	_, err = content.DeleteDiscussionCtx(ctx, s.DB, d.ID)
	if err != nil {
		respondInternalErr(c, "删除讨论失败。")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) renderDiscussionContent(content, format string) string {
	var html string
	switch format {
	case "md":
		html = parsers.RenderSafeMarkdown(content)
	case "bbcode":
		html = parsers.RenderBBCode(content)
	case "html":
		html = content
	default:
		html = parsers.RenderSafeMarkdown(content)
	}
	return s.rewriteStaticAssetURLs(html)
}
