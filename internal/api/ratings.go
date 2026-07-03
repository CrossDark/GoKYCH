package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gokych/internal/content"
)

func (s *Server) getRating(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	voterKey := ""
	if u := CurrentUserFromContext(c); u != nil {
		voterKey = content.VoterKey(&u.ID, u.Username)
	}
	ctx := c.Request.Context()
	rs, err := content.GetRatingSummaryCtx(ctx, s.DB, articleID, voterKey)
	if err != nil {
		respondInternalErr(c, "加载评分失败。")
		return
	}
	c.JSON(http.StatusOK, rs)
}

func (s *Server) setRating(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	var in struct {
		Score float64 `json:"score"`
	}
	if !bindJSON(c, &in) {
		return
	}
	if in.Score < -1 || in.Score > 1 {
		respondBadRequest(c, "评分范围需在 -1 到 1 之间。")
		return
	}
	var userID *int
	authorName := "匿名"
	if u := CurrentUserFromContext(c); u != nil {
		uid := u.ID
		userID = &uid
		authorName = u.Username
	}
	ctx := c.Request.Context()
	rs, err := content.SetRatingCtx(ctx, s.DB, articleID, userID, authorName, in.Score)
	if err != nil {
		respondInternalErr(c, "评分失败。")
		return
	}
	atype, slug := c.Param("type"), c.Param("slug")
	s.revalidateFrontend([]string{"article:" + atype + ":" + slug}, []string{"/" + atype + "/" + slug})
	c.JSON(http.StatusOK, rs)
}

func (s *Server) deleteRating(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	voterKey := ""
	if u := CurrentUserFromContext(c); u != nil {
		voterKey = content.VoterKey(&u.ID, u.Username)
	}
	if voterKey == "" {
		respondBadRequest(c, "需要登录。")
		return
	}
	ctx := c.Request.Context()
	deleted, err := content.DeleteRatingCtx(ctx, s.DB, articleID, voterKey)
	if err != nil {
		respondInternalErr(c, "取消评分失败。")
		return
	}
	if !deleted {
		respondNotFound(c, "未找到评分记录。")
		return
	}
	atype, slug := c.Param("type"), c.Param("slug")
	s.revalidateFrontend([]string{"article:" + atype + ":" + slug}, []string{"/" + atype + "/" + slug})
	rs, err := content.GetRatingSummaryCtx(ctx, s.DB, articleID, voterKey)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	c.JSON(http.StatusOK, rs)
}

func (s *Server) listRatings(c *gin.Context) {
	articleID, ok := s.requireArticleID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	ratings, err := content.ListRatingsCtx(ctx, s.DB, articleID)
	if err != nil {
		respondInternalErr(c, "加载评分记录失败。")
		return
	}
	if ratings == nil {
		ratings = []content.Rating{}
	}
	c.JSON(http.StatusOK, gin.H{"ratings": ratings})
}
