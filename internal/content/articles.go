package content

import (
	"database/sql"
	"log"
	"math"
	"strings"
	"time"
)

// Article represents a row from the unified articles table.
type Article struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	AuthorID  *int      `json:"author_id"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// ListArticles returns a page of articles, optionally filtered by type.
// Pagination is keyset-based: beforeID=0 returns the newest perPage rows,
// beforeID=N returns the perPage rows with id < N (older). This avoids the
// O(offset) scan of LIMIT/OFFSET on deep pages. Total/TotalPages are still
// computed (single COUNT) for UI page indicators; Page mirrors the requested
// page number for display.
func ListArticles(db *sql.DB, atype string, page, perPage, beforeID int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	if page < 1 {
		page = 1
	}

	var total int
	var rows *sql.Rows
	var err error

	if atype == "" {
		err = db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&total)
		if err != nil {
			return nil, err
		}
		if beforeID > 0 {
			rows, err = db.Query(
				`SELECT id, type, slug, title, LEFT(content, 200) AS content, author_id, created_at, updated_at
			 FROM articles WHERE id < ? ORDER BY id DESC LIMIT ?`,
				beforeID, perPage)
		} else {
			rows, err = db.Query(
				`SELECT id, type, slug, title, LEFT(content, 200) AS content, author_id, created_at, updated_at
			 FROM articles ORDER BY id DESC LIMIT ?`,
				perPage)
		}
	} else {
		err = db.QueryRow(`SELECT COUNT(*) FROM articles WHERE type = ?`, atype).Scan(&total)
		if err != nil {
			return nil, err
		}
		if beforeID > 0 {
			rows, err = db.Query(
				`SELECT id, type, slug, title, LEFT(content, 200) AS content, author_id, created_at, updated_at
			 FROM articles WHERE type = ? AND id < ? ORDER BY id DESC LIMIT ?`,
				atype, beforeID, perPage)
		} else {
			rows, err = db.Query(
				`SELECT id, type, slug, title, LEFT(content, 200) AS content, author_id, created_at, updated_at
			 FROM articles WHERE type = ? ORDER BY id DESC LIMIT ?`,
				atype, perPage)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Type, &a.Slug, &a.Title, &a.Content, &a.AuthorID, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	err := db.QueryRow(
		`SELECT id, type, slug, title, content, author_id, created_at, updated_at
		 FROM articles WHERE type = ? AND slug = ?`, atype, slug,
	).Scan(&a.ID, &a.Type, &a.Slug, &a.Title, &a.Content, &a.AuthorID, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
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
func CreateArticle(db *sql.DB, atype, slug, title, content string, authorID *int) (*Article, error) {
	_, err := db.Exec(
		`INSERT INTO articles (type, slug, title, content, author_id) VALUES (?, ?, ?, ?, ?)`,
		atype, slug, title, content, authorID,
	)
	if err != nil {
		return nil, err
	}
	return GetArticle(db, atype, slug)
}

// UpdateArticle updates title and content. Returns the updated article.
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
	// Invalidate the typst cache: the source changed, so any cached HTML is
	// stale. (Article deletion is handled by the ON DELETE CASCADE fk on
	// typst_cache, so DeleteArticle needs no extra step.)
	if _, derr := db.Exec(`DELETE FROM typst_cache WHERE article_id = ?`, a.ID); derr != nil {
		log.Printf("[content] warn: failed to invalidate typst_cache for article %d: %v", a.ID, derr)
	}
	return a, nil
}

// DeleteArticle removes an article and all associated data (CASCADE handles
// comments, ratings, article_tags, typst_files, typst_cache, featured).
func DeleteArticle(db *sql.DB, atype, slug string) (bool, error) {
	res, err := db.Exec(`DELETE FROM articles WHERE type = ? AND slug = ?`, atype, slug)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListRecentArticles returns the most recently updated articles across all types.
func ListRecentArticles(db *sql.DB, limit int) ([]Article, error) {
	rows, err := db.Query(
		`SELECT id, type, slug, title, LEFT(content, 200) AS content, author_id, created_at, updated_at
		 FROM articles ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var articles = make([]Article, 0)
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Type, &a.Slug, &a.Title, &a.Content, &a.AuthorID, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
		`SELECT id, type, slug, title, LEFT(content, 200) AS content, author_id, created_at, updated_at
		 FROM articles WHERE MATCH(title, content) AGAINST(?)
		 ORDER BY MATCH(title, content) AGAINST(?) DESC, updated_at DESC LIMIT ? OFFSET ?`,
		keyword, keyword, perPage, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.Type, &a.Slug, &a.Title, &a.Content, &a.AuthorID, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	return &ArticleListResult{
		Articles:   articles,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	}, nil
}
