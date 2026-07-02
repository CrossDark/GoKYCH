package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"gokych/internal/content/parsers"
	"gokych/internal/typst"
)

type anonRenderLookup struct {
	ctx         context.Context
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
	a, err := GetArticleCtx(l.ctx, l.db, atype, slug)
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

type anonUserLookup struct {
	ctx context.Context
	db  *sql.DB
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
	err := l.db.QueryRowContext(l.ctx,
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

func renderArticleAnonCtx(ctx context.Context, db *sql.DB, w *typst.Worker, a *Article) (html string, depIDs []int, err error) {
	switch a.Type {
	case "md", "bbcode", "html":
		rendered := parsers.Render(parsers.ArticleType(a.Type), a.ID, a.Content)
		return string(rendered), nil, nil

	case "wikidot":
		lookup := &anonRenderLookup{
			ctx:         ctx,
			db:          db,
			currentType: a.Type,
			currentSlug: a.Slug,
			currentID:   a.ID,
		}
		userLookup := &anonUserLookup{ctx: ctx, db: db}
		vars := buildAnonVars(a)
		renderCtx := &parsers.RenderContext{
			PageLookup:  lookup,
			UserLookup:  userLookup,
			Vars:        vars,
			ArticleType: a.Type,
			Typst:       w,
		}
		rendered := parsers.RenderCtx(parsers.ArticleType(a.Type), a.ID, a.Content, renderCtx)
		return string(rendered), lookup.depIDs, nil

	case "typst":
		if !typst.Available() {
			return parsers.PostProcessArticleHTML(`<p><em>Typst 编译器未安装。</em></p>`, "typst-content"), nil, nil
		}
		if w == nil {
			return parsers.PostProcessArticleHTML(`<p><em>本文档尚未编译完成,请稍后再试。</em></p>`, "typst-content"), nil, nil
		}
		body, err := w.CompileHTMLCached(a.ID, "")
		if err != nil {
			return "", nil, nil
		}
		return parsers.PostProcessArticleHTML(body, "typst-content"), nil, nil

	default:
		return `<p>不支持的格式。</p>`, nil, nil
	}
}

func renderArticleAnon(db *sql.DB, w *typst.Worker, a *Article) (html string, depIDs []int, err error) {
	return renderArticleAnonCtx(context.TODO(), db, w, a)
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

func saveRenderedHTMLCtx(ctx context.Context, db *sql.DB, articleID int, html string, depIDs []int) error {
	_, err := db.ExecContext(ctx,
		`UPDATE articles SET rendered_html = ? WHERE id = ?`,
		html, articleID,
	)
	if err != nil {
		return err
	}
	if _, derr := db.ExecContext(ctx, `DELETE FROM article_deps WHERE article_id = ?`, articleID); derr != nil {
		slog.Warn("rendercache: clear article_deps failed", "article_id", articleID, "err", derr)
	}
	for _, did := range depIDs {
		if _, ierr := db.ExecContext(ctx,
			`INSERT IGNORE INTO article_deps (article_id, depends_on_id) VALUES (?, ?)`,
			articleID, did,
		); ierr != nil {
			slog.Warn("rendercache: insert article_deps failed", "article_id", articleID, "dep_id", did, "err", ierr)
		}
	}
	return nil
}

func saveRenderedHTML(db *sql.DB, articleID int, html string, depIDs []int) error {
	return saveRenderedHTMLCtx(context.TODO(), db, articleID, html, depIDs)
}

func invalidateCacheOneCtx(ctx context.Context, db *sql.DB, articleID int) ([]int, error) {
	_, err := db.ExecContext(ctx, `UPDATE articles SET rendered_html = NULL WHERE id = ?`, articleID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT article_id FROM article_deps WHERE depends_on_id = ?`, articleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			deps = append(deps, id)
		}
	}
	return deps, rows.Err()
}

func invalidateCacheOne(db *sql.DB, articleID int) ([]int, error) {
	return invalidateCacheOneCtx(context.TODO(), db, articleID)
}

func InvalidateCacheCascadingCtx(ctx context.Context, db *sql.DB, articleID int) []int {
	visited := map[int]bool{articleID: true}
	queue := []int{articleID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		deps, err := invalidateCacheOneCtx(ctx, db, cur)
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

// Deprecated: Use InvalidateCacheCascadingCtx instead.
func InvalidateCacheCascading(db *sql.DB, articleID int) []int {
	return InvalidateCacheCascadingCtx(context.TODO(), db, articleID)
}

func RenderAndSaveCtx(ctx context.Context, db *sql.DB, w *typst.Worker, a *Article) error {
	html, deps, err := renderArticleAnonCtx(ctx, db, w, a)
	if err != nil {
		return err
	}
	if a.Type == "typst" && html == "" {
		return nil
	}
	return saveRenderedHTMLCtx(ctx, db, a.ID, html, deps)
}

// Deprecated: Use RenderAndSaveCtx instead.
func RenderAndSave(db *sql.DB, w *typst.Worker, a *Article) error {
	return RenderAndSaveCtx(context.TODO(), db, w, a)
}

func UpdateTypstHTMLCtx(ctx context.Context, db *sql.DB, articleID int, rawHTML string) error {
	processed := parsers.PostProcessArticleHTML(rawHTML, "typst-content")
	_, err := db.ExecContext(ctx, `UPDATE articles SET rendered_html = ? WHERE id = ?`, processed, articleID)
	return err
}

// Deprecated: Use UpdateTypstHTMLCtx instead.
func UpdateTypstHTML(db *sql.DB, articleID int, rawHTML string) error {
	return UpdateTypstHTMLCtx(context.TODO(), db, articleID, rawHTML)
}

func WarmCacheCtx(ctx context.Context, db *sql.DB, w *typst.Worker, batchSize int) int {
	rows, err := db.QueryContext(ctx,
		`SELECT id, type, slug FROM articles
		 WHERE rendered_html IS NULL AND type != 'typst'
		 ORDER BY updated_at DESC LIMIT ?`, batchSize,
	)
	if err != nil {
		slog.Error("rendercache: warm query failed", "err", err)
		return 0
	}
	type ref struct {
		id          int
		atype, slug string
	}
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
	rows.Close()
	count := 0
	for _, r := range refs {
		a, err := GetArticleCtx(ctx, db, r.atype, r.slug)
		if err != nil {
			continue
		}
		if err := RenderAndSaveCtx(ctx, db, w, a); err != nil {
			slog.Warn("rendercache: render failed", "article_id", a.ID, "slug", a.Slug, "err", err)
			continue
		}
		count++
	}

	trows, terr := db.QueryContext(ctx,
		`SELECT a.id, tc.html_content
		 FROM articles a JOIN typst_cache tc ON tc.article_id = a.id
		 WHERE a.rendered_html IS NULL AND a.type = 'typst'
		 LIMIT ?`, batchSize,
	)
	if terr == nil {
		for trows.Next() {
			var id int
			var rawHTML string
			if err := trows.Scan(&id, &rawHTML); err == nil && rawHTML != "" {
				processed := parsers.PostProcessArticleHTML(rawHTML, "typst-content")
				if _, err := db.ExecContext(ctx, `UPDATE articles SET rendered_html = ? WHERE id = ?`, processed, id); err == nil {
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

// Deprecated: Use WarmCacheCtx instead.
func WarmCache(db *sql.DB, w *typst.Worker, batchSize int) int {
	return WarmCacheCtx(context.TODO(), db, w, batchSize)
}

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
