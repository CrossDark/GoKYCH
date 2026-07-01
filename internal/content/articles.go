package content

import (
	"database/sql"
	"log/slog"
	"math"
	"strings"
	"time"

	"gokych/internal/typst"
)

// Article represents a row from the unified articles table.
type Article struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	AuthorID  *int      `json:"author_id"`
	// Author* are LEFT-JOINed from users so articles with no author (or with
	// an author whose user row was deleted) still serialise cleanly. All
	// three use omitempty so the front-end can render the "no author"
	// branch just by checking AuthorName === "".
	AuthorName     string `json:"author_name,omitempty"`
	AuthorNickname string `json:"author_nickname,omitempty"`
	AuthorAvatar   string `json:"author_avatar,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ArticleListResult holds a paginated list of articles.
type ArticleListResult struct {
	Articles   []Article `json:"articles"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalPages int       `json:"total_pages"`
	// NextBefore is the id of the last article in this page; pass it as the
	// `before` query param to fetch the next page (keyset pagination). 0 means
	// no more pages. Populated by ListArticles; other builders leave it 0.
	NextBefore int `json:"next_before,omitempty"`
}

// scanArticleWithUser scans an article row with LEFT JOINed user fields
// (username, nickname, avatar). Used by both QueryRow and *sql.Rows scanners
// to eliminate the 11-field Scan + 3 NullString assignments repeated across
// ListArticles, GetArticle, ListRecentArticles, and SearchArticles.
//
// The scanner interface is satisfied by both *sql.Row and *sql.Rows so the
// same helper works for single-row and multi-row result sets. The withContent
// flag controls whether the content column is expected in the result (list
// queries use LEFT(content, 200), GetArticle uses full content — the column
// name alias is "content" in both cases so Scan still works).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanArticleWithUser(s rowScanner, a *Article) error {
	var username, nickname, avatar sql.NullString
	if err := s.Scan(
		&a.ID, &a.Type, &a.Slug, &a.Title, &a.Content, &a.AuthorID,
		&username, &nickname, &avatar, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return err
	}
	a.AuthorName = username.String
	a.AuthorNickname = nickname.String
	a.AuthorAvatar = avatar.String
	return nil
}

// ListArticles returns a page of articles, optionally filtered by type and/or
// author. Pass atype="" and authorID=nil for "no filter". Pagination is
// keyset-based: beforeID=0 returns the newest perPage rows, beforeID=N returns
// the perPage rows with id < N (older). This avoids the O(offset) scan of
// LIMIT/OFFSET on deep pages. Total/TotalPages are still computed (single
// COUNT) for UI page indicators; Page mirrors the requested page number for
// display.
//
// authorID semantics:
//   - nil  → no filter (all authors)
//   - non-nil → filter by author_id; the *int value is bound directly to the
//     SQL parameter, so a nil-valued *int (-1 / 0 with isNull=true) is not
//     representable here. Callers wanting "only articles with no author" can
//     pass a *int that points to a sentinel, or wrap the query — we use the
//     simple "id = ?" form for now, and the public API doesn't expose the
//     "unauthored" filter (the "我的文章" UI only ever asks for the caller's
//     own ID).
func ListArticles(db *sql.DB, atype string, authorID *int, page, perPage, beforeID int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	if page < 1 {
		page = 1
	}

	// Build the WHERE clause + bind list once, then reuse it for both the
	// COUNT and the SELECT. Two-arm if/else (with/without type filter) keeps
	// the SQL readable — the alternative is fmt.Sprintf-templated SQL, which
	// is harder to grep for and easy to get the bind order wrong.
	var (
		whereSQL string
		args     []any
	)
	if atype != "" {
		whereSQL += "type = ?"
		args = append(args, atype)
	}
	if authorID != nil {
		if whereSQL != "" {
			whereSQL += " AND "
		}
		whereSQL += "author_id = ?"
		args = append(args, *authorID)
	}
	if whereSQL == "" {
		whereSQL = "1=1"
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM articles WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	// Build the keyset-filtered list query. The keyset clause is conditional,
	// so the bind list has to grow with it — Go's database/sql rejects a
	// mismatch between `?` count and arg count, so we always bind the limit
	// but only bind the cursor when the keyset clause is present.
	// LEFT JOIN users so author_name/nickname/avatar come back in one query
	// (avoids the N+1 trap of fetching every author separately for the
	// homepage list). Anonymous / deleted-author rows surface with empty
	// author_* — checked via omitempty on the front-end.
	listSQL := `SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, a.author_id,
		            u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id WHERE ` + whereSQL
	listArgs := append(append([]any{}, args...), perPage)
	if beforeID > 0 {
		listSQL += " AND a.id < ?"
		// Insert beforeID before the trailing limit, matching the new `?` we
		// just appended to the SQL.
		listArgs = append(listArgs[:len(listArgs)-1], beforeID, perPage)
	}
	listSQL += " ORDER BY a.id DESC LIMIT ?"

	rows, err := db.Query(listSQL, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		if err := scanArticleWithUser(rows, &a); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := int(math.Ceil(float64(total) / float64(perPage)))
	if totalPages < 1 {
		totalPages = 1
	}

	// Batch-fetch tags for all articles.
	tagMap, err := GetTagsForArticlesBatch(db, articles)
	if err != nil {
		return nil, err
	}
	for i := range articles {
		articles[i].Tags = tagMap[articles[i].ID]
	}

	// Keyset cursor: the id of the last (oldest) row. 0 (empty page) signals
	// the client there's no next page to fetch.
	nextBefore := 0
	if n := len(articles); n == perPage {
		nextBefore = articles[n-1].ID
	}

	return &ArticleListResult{
		Articles:   articles,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
		NextBefore: nextBefore,
	}, nil
}

// GetArticle loads a single article by type and slug.
func GetArticle(db *sql.DB, atype, slug string) (*Article, error) {
	a := &Article{}
	row := db.QueryRow(
		`SELECT a.id, a.type, a.slug, a.title, a.content, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id
		 WHERE a.type = ? AND a.slug = ?`, atype, slug,
	)
	if err := scanArticleWithUser(row, a); err != nil {
		return nil, err
	}
	tags, err := GetTagsForArticle(db, a.ID)
	if err != nil {
		return nil, err
	}
	a.Tags = tags
	return a, nil
}

// CreateArticle inserts a new article and returns it.
// For typst articles, compilation is enqueued asynchronously instead of
// blocking the request — readers will see a "compiling..." placeholder until
// the background worker finishes.
func CreateArticle(db *sql.DB, atype, slug, title, content string, authorID *int) (*Article, error) {
	_, err := db.Exec(
		`INSERT INTO articles (type, slug, title, content, author_id) VALUES (?, ?, ?, ?, ?)`,
		atype, slug, title, content, authorID,
	)
	if err != nil {
		return nil, err
	}
	a, err := GetArticle(db, atype, slug)
	if err != nil {
		return nil, err
	}
	// Enqueue async typst compilation (non-blocking; errors logged but don't
	// fail the create — the queue row shows 'failed' with CLI-not-found msg
	// if typst isn't installed).
	if atype == "typst" {
		if qerr := typst.EnqueueCompile(db, a.ID); qerr != nil {
			slog.Warn("failed to enqueue typst compile on create", "article_id", a.ID, "err", qerr)
		}
	}
	return a, nil
}

// UpdateArticle updates title and content. Returns the updated article.
// For typst articles, the old cache is invalidated and a fresh async
// compilation is enqueued; dependents are also re-queued so their compiled
// output picks up the changes.
func UpdateArticle(db *sql.DB, atype, slug, title, content string) (*Article, error) {
	_, err := db.Exec(
		`UPDATE articles SET title = ?, content = ? WHERE type = ? AND slug = ?`,
		title, content, atype, slug,
	)
	if err != nil {
		return nil, err
	}
	a, err := GetArticle(db, atype, slug)
	if err != nil {
		return nil, err
	}
	// Enqueue async re-compile for typst. The old cache is deleted so readers
	// immediately see the "compiling..." placeholder instead of stale HTML.
	if atype == "typst" {
		if _, derr := db.Exec(`DELETE FROM typst_cache WHERE article_id = ?`, a.ID); derr != nil {
			slog.Warn("failed to invalidate typst_cache", "article_id", a.ID, "err", derr)
		}
		if qerr := typst.EnqueueCompile(db, a.ID); qerr != nil {
			slog.Warn("failed to enqueue typst compile on update", "article_id", a.ID, "err", qerr)
		}
		// Cascade: any article that @imports this one (directly or
		// transitively) must also be re-compiled against the new source.
		if ierr := typst.EnqueueDependents(db, a.ID); ierr != nil {
			slog.Warn("failed to enqueue typst dependents", "article_id", a.ID, "err", ierr)
		}
	}
	return a, nil
}

// DeleteArticle removes an article and all associated data (CASCADE handles
// comments, ratings, article_tags, typst_files, typst_cache, featured, and
// compile_queue entries).
func DeleteArticle(db *sql.DB, atype, slug string) (bool, error) {
	// Load the article first so we can cascade-invalidate typst dependents
	// before the row is gone (CASCADE will delete typst_cache but not
	// invalidate or re-queue other articles' caches that depend on this one).
	a, _ := GetArticle(db, atype, slug)
	res, err := db.Exec(`DELETE FROM articles WHERE type = ? AND slug = ?`, atype, slug)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 && a != nil && atype == "typst" {
		// Cascade: enqueue dependents for re-compilation (their cached HTML
		// imported this article and now needs updating).
		if ierr := typst.EnqueueDependents(db, a.ID); ierr != nil {
			slog.Warn("failed to enqueue typst dependents on delete", "article_id", a.ID, "err", ierr)
		}
	}
	return n > 0, nil
}

// ListRecentArticles returns the most recently updated articles across all types.
func ListRecentArticles(db *sql.DB, limit int) ([]Article, error) {
	rows, err := db.Query(
		`SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id
		 ORDER BY a.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var articles = make([]Article, 0)
	for rows.Next() {
		var a Article
		if err := scanArticleWithUser(rows, &a); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Batch-fetch tags for all articles.
	tagMap, err := GetTagsForArticlesBatch(db, articles)
	if err != nil {
		return nil, err
	}
	for i := range articles {
		articles[i].Tags = tagMap[articles[i].ID]
	}

	return articles, nil
}

// SearchArticles performs a full-text search across all articles.
func SearchArticles(db *sql.DB, q string, page, perPage int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	// NATURAL LANGUAGE MODE: no boolean operators to escape (P2-16), and rows
	// rank by relevance so we ORDER BY the MATCH score (P2-21). An empty query
	// would error in MySQL, so short-circuit to an empty page.
	keyword := strings.TrimSpace(q)
	offset := (page - 1) * perPage
	if keyword == "" {
		return &ArticleListResult{
			Articles:   make([]Article, 0),
			Total:      0,
			Page:       page,
			PerPage:    perPage,
			TotalPages: 1,
		}, nil
	}

	var total int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM articles WHERE MATCH(title, content) AGAINST(?)`,
		keyword,
	).Scan(&total)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id
		 WHERE MATCH(a.title, a.content) AGAINST(?)
		 ORDER BY MATCH(a.title, a.content) AGAINST(?) DESC, a.updated_at DESC LIMIT ? OFFSET ?`,
		keyword, keyword, perPage, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		if err := scanArticleWithUser(rows, &a); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Batch-fetch tags for all articles.
	tagMap, err := GetTagsForArticlesBatch(db, articles)
	if err != nil {
		return nil, err
	}
	for i := range articles {
		articles[i].Tags = tagMap[articles[i].ID]
	}

	totalPages := int(math.Ceil(float64(total) / float64(perPage)))
	if totalPages < 1 {
		totalPages = 1
	}
	return &ArticleListResult{
		Articles:   articles,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}, nil
}
