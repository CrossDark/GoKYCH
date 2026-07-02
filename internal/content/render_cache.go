package content

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"gokych/internal/content/parsers"
	"gokych/internal/typst"
)

// ── PageLookup for anon pre-render ──────────────────────────────────

type anonRenderLookup struct {
	db          *sql.DB
	currentType string
	currentSlug string
	currentID   int
	depIDs      []int
}

func (l *anonRenderLookup) IncludeBySlug(atype, slug string) *parsers.IncludedPage {
	if l == nil || l.db == nil {
		return nil
	}
	slug = strings.TrimSpace(slug)
	if atype == "" {
		atype = l.currentType
	}
	if atype == l.currentType && slug == l.currentSlug {
		return nil
	}
	a, err := GetArticle(l.db, atype, slug)
	if err != nil {
		return nil
	}
	found := false
	for _, id := range l.depIDs {
		if id == a.ID {
			found = true
			break
		}
	}
	if !found {
		l.depIDs = append(l.depIDs, a.ID)
	}
	return &parsers.IncludedPage{
		Type:    a.Type,
		Content: a.Content,
		Title:   a.Title,
	}
}

func (l *anonRenderLookup) ListPages(string, int, string) []parsers.ListPageEntry { return nil }
func (l *anonRenderLookup) RandomPage(string) *parsers.ListPageEntry              { return nil }

// ── UserLookup for anon pre-render ──────────────────────────────────

type anonUserLookup struct {
	db *sql.DB
}

func (l *anonUserLookup) UserByName(name string) *parsers.UserProfile {
	if l == nil || l.db == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var (
		id       int64
		username sql.NullString
		nickname sql.NullString
		role     sql.NullString
		avatar   sql.NullString
	)
	err := l.db.QueryRow(
		`SELECT id, username, nickname, role, avatar FROM users
		 WHERE LOWER(username) = LOWER(?) LIMIT 1`, name,
	).Scan(&id, &username, &nickname, &role, &avatar)
	if err != nil {
		return nil
	}
	return &parsers.UserProfile{
		ID:        id,
		Username:  username.String,
		Nickname:  nickname.String,
		AvatarURL: avatar.String,
		IsStaff:   role.String == "admin" || role.String == "owner",
	}
}

// ── Public render-cache API ─────────────────────────────────────────

func renderArticleAnon(db *sql.DB, a *Article) (html string, depIDs []int, err error) {
	switch a.Type {
	case "md", "bbcode", "html":
		rendered := parsers.Render(parsers.ArticleType(a.Type), a.ID, a.Content)
		return string(rendered), nil, nil

	case "wikidot":
		lookup := &anonRenderLookup{
			db:          db,
			currentType: a.Type,
			currentSlug: a.Slug,
			currentID:   a.ID,
		}
		userLookup := &anonUserLookup{db: db}
		vars := buildAnonVars(a)
		ctx := &parsers.RenderContext{
			PageLookup:  lookup,
			UserLookup:  userLookup,
			Vars:        vars,
			ArticleType: a.Type,
		}
		rendered := parsers.RenderCtx(parsers.ArticleType(a.Type), a.ID, a.Content, ctx)
		return string(rendered), lookup.depIDs, nil

	case "typst":
		if !typst.Available() {
			return parsers.PostProcessArticleHTML(`<p><em>Typst 编译器未安装。</em></p>`, "typst-content"), nil, nil
		}
		body, err := typst.CompileHTMLCached(a.ID, "")
		if err != nil {
			return "", nil, nil
		}
		return parsers.PostProcessArticleHTML(body, "typst-content"), nil, nil

	default:
		return `<p>不支持的格式。</p>`, nil, nil
	}
}

func buildAnonVars(a *Article) map[string]string {
	ratingHint := "请使用页面内置评分"
	vars := map[string]string{
		"title":           a.Title,
		"slug":            a.Slug,
		"author_name":     a.AuthorName,
		"author_nickname": a.AuthorNickname,
		"created_at":      a.CreatedAt.Format("2006-01-02"),
		"updated_at":      a.UpdatedAt.Format("2006-01-02"),
		"tags":            strings.Join(a.Tags, ", "),
		"user_name":       "anonymous",
		"user_nickname":   "anonymous",
		"user_id":         "",
		"is_admin":        "0",
		"is_owner":        "0",
		"rating":          ratingHint,
		"rating_count":    ratingHint,
	}
	return vars
}

func saveRenderedHTML(db *sql.DB, articleID int, html string, depIDs []int) error {
	_, err := db.Exec(
		`UPDATE articles SET rendered_html = ? WHERE id = ?`,
		html, articleID,
	)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`DELETE FROM article_deps WHERE article_id = ?`, articleID)
	for _, did := range depIDs {
		_, _ = db.Exec(
			`INSERT IGNORE INTO article_deps (article_id, depends_on_id) VALUES (?, ?)`,
			articleID, did,
		)
	}
	return nil
}

// invalidateCacheOne clears rendered_html for a single article and returns
// IDs of articles that DIRECTLY depend on it (one level — BFS caller
// InvalidateCacheCascading walks transitively).
func invalidateCacheOne(db *sql.DB, articleID int) ([]int, error) {
	_, err := db.Exec(`UPDATE articles SET rendered_html = NULL WHERE id = ?`, articleID)
	if err != nil {
		return nil, err
	}
	depSet := map[int]bool{}
	rows, err := db.Query(
		`SELECT article_id FROM article_deps WHERE depends_on_id = ?`, articleID,
	)
	if err == nil {
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err == nil {
				depSet[id] = true
			}
		}
		rows.Close()
	}
	rows2, err := db.Query(
		`SELECT article_id FROM typst_cache WHERE dependencies LIKE ?`,
		`%`+fmt.Sprintf("%d", articleID)+`%`,
	)
	if err == nil {
		for rows2.Next() {
			var id int
			if err := rows2.Scan(&id); err == nil {
				depSet[id] = true
			}
		}
		rows2.Close()
	}
	deps := make([]int, 0, len(depSet))
	for id := range depSet {
		deps = append(deps, id)
	}
	return deps, nil
}

// InvalidateCacheCascading BFS-clears rendered_html for the article
// and all transitive dependents.
func InvalidateCacheCascading(db *sql.DB, articleID int) []int {
	visited := map[int]bool{articleID: true}
	queue := []int{articleID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		deps, err := invalidateCacheOne(db, cur)
		if err != nil {
			slog.Warn("rendercache: invalidate failed", "article_id", cur, "err", err)
			continue
		}
		for _, d := range deps {
			if !visited[d] {
				visited[d] = true
				queue = append(queue, d)
			}
		}
	}
	result := make([]int, 0, len(visited))
	for id := range visited {
		result = append(result, id)
	}
	return result
}

// RenderAndSave renders the article for anon view and persists the result.
func RenderAndSave(db *sql.DB, a *Article) error {
	html, deps, err := renderArticleAnon(db, a)
	if err != nil {
		return err
	}
	if a.Type == "typst" && html == "" {
		return nil
	}
	return saveRenderedHTML(db, a.ID, html, deps)
}

// UpdateTypstHTML is called by the typst background worker after a
// successful compilation, to sync the post-processed compiled HTML into
// articles.rendered_html.
func UpdateTypstHTML(db *sql.DB, articleID int, rawHTML string) error {
	processed := parsers.PostProcessArticleHTML(rawHTML, "typst-content")
	_, err := db.Exec(`UPDATE articles SET rendered_html = ? WHERE id = ?`, processed, articleID)
	return err
}

// WarmCache backfills rendered_html for articles where it's still NULL.
func WarmCache(db *sql.DB, batchSize int) int {
	rows, err := db.Query(
		`SELECT id, type, slug FROM articles
		 WHERE rendered_html IS NULL AND type != 'typst'
		 ORDER BY updated_at DESC LIMIT ?`, batchSize,
	)
	if err != nil {
		slog.Error("rendercache: warm query failed", "err", err)
		return 0
	}
	defer rows.Close()
	type ref struct{ id int; atype, slug string }
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.atype, &r.slug); err == nil {
			refs = append(refs, r)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rendercache: warm rows error", "err", err)
	}
	count := 0
	for _, r := range refs {
		a, err := GetArticle(db, r.atype, r.slug)
		if err != nil {
			continue
		}
		if err := RenderAndSave(db, a); err != nil {
			slog.Warn("rendercache: render failed", "article_id", a.ID, "slug", a.Slug, "err", err)
			continue
		}
		count++
	}
	rows.Close()

	trows, terr := db.Query(
		`SELECT a.id, tc.html_content
		 FROM articles a JOIN typst_cache tc ON tc.article_id = a.id
		 WHERE a.rendered_html IS NULL AND a.type = 'typst'
		 LIMIT ?`, batchSize,
	)
	if terr == nil {
		defer trows.Close()
		for trows.Next() {
			var id int
			var rawHTML string
			if err := trows.Scan(&id, &rawHTML); err == nil && rawHTML != "" {
				processed := parsers.PostProcessArticleHTML(rawHTML, "typst-content")
				if _, err := db.Exec(`UPDATE articles SET rendered_html = ? WHERE id = ?`, processed, id); err == nil {
					count++
				}
			}
		}
		trows.Close()
	}

	if count > 0 {
		slog.Info("rendercache: warm-up completed", "rendered", count)
	}
	return count
}

// DepsTableDDL returns the CREATE TABLE for article_deps.
func DepsTableDDL() string {
	return `CREATE TABLE IF NOT EXISTS article_deps (
		article_id    INT NOT NULL,
		depends_on_id INT NOT NULL,
		PRIMARY KEY (article_id, depends_on_id),
		INDEX idx_depends_on (depends_on_id),
		FOREIGN KEY (article_id)    REFERENCES articles(id) ON DELETE CASCADE,
		FOREIGN KEY (depends_on_id) REFERENCES articles(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
}

var reWikidotInclude = regexp.MustCompile(`(?is)\[\[include\s+([^\s\|\]]+)`)

func extractWikidotIncludeSlugs(source string) []string {
	matches := reWikidotInclude.FindAllStringSubmatch(source, -1)
	seen := map[string]bool{}
	var slugs []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		slug := strings.TrimSpace(m[1])
		if i := strings.Index(slug, ":"); i >= 0 {
			slug = slug[i+1:]
		}
		if slug != "" && !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

func articleTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}
