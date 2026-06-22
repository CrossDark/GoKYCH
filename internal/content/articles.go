package content

import (
	"database/sql"
	"math"
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
}

// ListArticles returns paginated articles, optionally filtered by type.
// If atype is empty, returns all types.
func ListArticles(db *sql.DB, atype string, page, perPage int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	offset := (page - 1) * perPage

	var total int
	var rows *sql.Rows
	var err error

	if atype == "" {
		err = db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&total)
		if err != nil {
			return nil, err
		}
		rows, err = db.Query(
			`SELECT id, type, slug, title, content, author_id, created_at, updated_at
			 FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			perPage, offset)
	} else {
		err = db.QueryRow(`SELECT COUNT(*) FROM articles WHERE type = ?`, atype).Scan(&total)
		if err != nil {
			return nil, err
		}
		rows, err = db.Query(
			`SELECT id, type, slug, title, content, author_id, created_at, updated_at
			 FROM articles WHERE type = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			atype, perPage, offset)
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

	return &ArticleListResult{
		Articles:   articles,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
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
	return GetArticle(db, atype, slug)
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
		`SELECT id, type, slug, title, content, author_id, created_at, updated_at
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
	keyword := q + "*"
	offset := (page - 1) * perPage

	var total int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM articles WHERE MATCH(title, content) AGAINST(? IN BOOLEAN MODE)`,
		keyword,
	).Scan(&total)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT id, type, slug, title, content, author_id, created_at, updated_at
		 FROM articles WHERE MATCH(title, content) AGAINST(? IN BOOLEAN MODE)
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		keyword, perPage, offset,
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
