package content

import (
	"database/sql"
	"math"
)

// Tag represents a tag row.
type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// TagWithCount includes an article count.
type TagWithCount struct {
	Tag
	Count int `json:"count"`
}

// GetOrCreateTag ensures a tag exists and returns its ID.
func GetOrCreateTag(db *sql.DB, name string) (int, error) {
	var id int
	err := db.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	res, err := db.Exec(`INSERT INTO tags (name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	lid, _ := res.LastInsertId()
	return int(lid), nil
}

// GetAllTagsWithCounts returns all tags with article counts.
func GetAllTagsWithCounts(db *sql.DB) ([]TagWithCount, error) {
	rows, err := db.Query(
		`SELECT t.id, t.name, COUNT(at.tag_id) AS cnt
		 FROM tags t LEFT JOIN article_tags at ON t.id = at.tag_id
		 GROUP BY t.id, t.name ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out = make([]TagWithCount, 0)
	for rows.Next() {
		var t TagWithCount
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTagsForArticle returns tag names for an article.
func GetTagsForArticle(db *sql.DB, articleID int) ([]string, error) {
	rows, err := db.Query(
		`SELECT t.name FROM tags t
		 JOIN article_tags at ON t.id = at.tag_id
		 WHERE at.article_id = ? ORDER BY t.name`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// GetTagsForArticlesBatch returns a map of articleID → tag names.
func GetTagsForArticlesBatch(db *sql.DB, articles []Article) (map[int][]string, error) {
	if len(articles) == 0 {
		return nil, nil
	}
	ids := make([]int, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	// Build IN clause
	query := `SELECT at.article_id, t.name FROM article_tags at
		JOIN tags t ON t.id = at.tag_id
		WHERE at.article_id IN (` + placeholders(len(ids)) + `) ORDER BY t.name`
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int][]string)
	for rows.Next() {
		var aid int
		var name string
		if err := rows.Scan(&aid, &name); err != nil {
			return nil, err
		}
		out[aid] = append(out[aid], name)
	}
	return out, rows.Err()
}

// SetArticleTags replaces all tags for an article.
func SetArticleTags(db *sql.DB, articleID int, tagNames []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM article_tags WHERE article_id = ?`, articleID); err != nil {
		return err
	}
	for _, name := range tagNames {
		tagID, err := GetOrCreateTag(db, name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO article_tags (article_id, tag_id) VALUES (?, ?)`, articleID, tagID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetArticlesByTag returns paginated articles for a tag.
func GetArticlesByTag(db *sql.DB, tagName string, page, perPage int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	offset := (page - 1) * perPage

	var total int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM article_tags at
		 JOIN tags t ON t.id = at.tag_id
		 JOIN articles a ON a.id = at.article_id
		 WHERE t.name = ?`, tagName,
	).Scan(&total)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT a.id, a.type, a.slug, a.title, a.content, a.author_id, a.created_at, a.updated_at
		 FROM articles a
		 JOIN article_tags at ON a.id = at.article_id
		 JOIN tags t ON t.id = at.tag_id
		 WHERE t.name = ?
		 ORDER BY a.created_at DESC LIMIT ? OFFSET ?`,
		tagName, perPage, offset,
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
	}, rows.Err()
}

// placeholders returns a comma-separated "?,?,?" string.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}
