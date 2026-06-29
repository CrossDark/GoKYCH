package api

import (
	"fmt"
	"strings"

	"gokych/internal/auth/user"
	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

// buildArticleVars composes the `%%name%%` substitution table
// passed to the wikidot renderer. Authors can reference any of
// these in their articles; the keys are stable across renders
// and documented in the wiki authoring guide.
//
// The full key set today:
//   - `%%title%%`          article title
//   - `%%slug%%`           URL slug
//   - `%%author_name%%`    author username (or "" if no author)
//   - `%%author_nickname%%` author display name (or "" if no author)
//   - `%%created_at%%`     created-at date in YYYY-MM-DD form
//   - `%%updated_at%%`     updated-at date in YYYY-MM-DD form
//   - `%%tags%%`           comma-separated tag list
//   - `%%rating%%`         current average rating, one decimal
//   - `%%rating_count%%`   number of distinct raters
//   - `%%user_name%%`      current viewer's username (or "anonymous")
//   - `%%user_nickname%%`  current viewer's display name (or "anonymous")
//   - `%%user_id%%`        current viewer's id (or "")
//   - `%%is_admin%%`       "1" if the viewer is admin/owner, else "0"
//   - `%%is_owner%%`       "1" if the viewer is the article's author, else "0"
//
// The split between "article-static" and "viewer" vars is
// internal: from the renderer's perspective they're all just
// `%%name%%` tokens. We pre-compute both halves here so the
// per-render path is a single map allocation.
func buildArticleVars(a *content.Article, u *user.User, rating *content.RatingSummary) map[string]string {
	vars := make(map[string]string, 16)
	vars["title"] = a.Title
	vars["slug"] = a.Slug
	vars["author_name"] = a.AuthorName
	vars["author_nickname"] = a.AuthorNickname
	vars["created_at"] = a.CreatedAt.Format("2006-01-02")
	vars["updated_at"] = a.UpdatedAt.Format("2006-01-02")
	vars["tags"] = strings.Join(a.Tags, ", ")
	if rating != nil {
		vars["rating"] = fmt.Sprintf("%.1f", rating.Average)
		vars["rating_count"] = fmt.Sprintf("%d", rating.TotalVoters)
	} else {
		vars["rating"] = "0.0"
		vars["rating_count"] = "0"
	}
	if u != nil {
		vars["user_name"] = u.Username
		vars["user_nickname"] = u.Nickname
		vars["user_id"] = fmt.Sprintf("%d", u.ID)
		vars["is_admin"] = "0"
		vars["is_owner"] = "0"
		if user.IsAdmin(u.Role) {
			vars["is_admin"] = "1"
		}
		if a.AuthorID != nil && *a.AuthorID == u.ID {
			vars["is_owner"] = "1"
		}
	} else {
		vars["user_name"] = "anonymous"
		vars["user_nickname"] = "anonymous"
		vars["user_id"] = ""
		vars["is_admin"] = "0"
		vars["is_owner"] = "0"
	}
	return vars
}

// Compile-time assertion that the helper signature matches
// the inline expectation in getArticle. Pulled out as a
// top-level function so it can be unit-tested later.
var _ = buildArticleVars

// silence unused-import warning if buildArticleVars ever
// stops referencing parsers (the parsers import is here for
// future var-marshalling helpers — `RatingSummary` lives
// in `content` but var keys are documented in the parser).
var _ = parsers.TypeWikidot
