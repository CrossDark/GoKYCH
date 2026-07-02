package content

import (
	"context"
	"database/sql"
	"math"

	coredb "gokych/internal/core/db"
	"gokych/internal/typst"
)

type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TagWithCount struct {
	Tag
	Count int `json:"count"`
}

type Querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

type QuerierCtx interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func GetOrCreateTagCtx(ctx context.Context, q QuerierCtx, name string) (int, error) {
	var id int
	err := q.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	res, err := q.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	lid, _ := res.LastInsertId()
	return int(lid), nil
}

// Deprecated: Use GetOrCreateTagCtx instead.
func GetOrCreateTag(q Querier, name string) (int, error) {
	return getOrCreateTagDeprecated(q, name)
}

func getOrCreateTagDeprecated(q Querier, name string) (int, error) {
	var id int
	err := q.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	res, err := q.Exec(`INSERT INTO tags (name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	lid, _ := res.LastInsertId()
	return int(lid), nil
}

func GetAllTagsWithCountsCtx(ctx context.Context, db *sql.DB) ([]TagWithCount, error) {
	rows, err := db.QueryContext(ctx,
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

// Deprecated: Use GetAllTagsWithCountsCtx instead.
func GetAllTagsWithCounts(db *sql.DB) ([]TagWithCount, error) {
	return GetAllTagsWithCountsCtx(context.TODO(), db)
}

func GetTagsForArticleCtx(ctx context.Context, db *sql.DB, articleID int) ([]string, error) {
	rows, err := db.QueryContext(ctx,
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

// Deprecated: Use GetTagsForArticleCtx instead.
func GetTagsForArticle(db *sql.DB, articleID int) ([]string, error) {
	return GetTagsForArticleCtx(context.TODO(), db, articleID)
}

func GetTagsForArticlesBatchCtx(ctx context.Context, db *sql.DB, articles []Article) (map[int][]string, error) {
	if len(articles) == 0 {
		return nil, nil
	}
	ids := make([]int, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	query := `SELECT at.article_id, t.name FROM article_tags at
		JOIN tags t ON t.id = at.tag_id
		WHERE at.article_id IN (` + coredb.Placeholders(len(ids)) + `) ORDER BY t.name`
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, query, args...)
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

// Deprecated: Use GetTagsForArticlesBatchCtx instead.
func GetTagsForArticlesBatch(db *sql.DB, articles []Article) (map[int][]string, error) {
	return GetTagsForArticlesBatchCtx(context.TODO(), db, articles)
}

func SetArticleTagsCtx(ctx context.Context, db *sql.DB, w *typst.Worker, articleID int, tagNames []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM article_tags WHERE article_id = ?`, articleID); err != nil {
		return err
	}
	for _, name := range tagNames {
		tagID, err := GetOrCreateTagCtx(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO article_tags (article_id, tag_id) VALUES (?, ?)`, articleID, tagID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	InvalidateArticleCacheCtx(ctx, db, w, articleID)
	return nil
}

// Deprecated: Use SetArticleTagsCtx instead.
func SetArticleTags(db *sql.DB, w *typst.Worker, articleID int, tagNames []string) error {
	return SetArticleTagsCtx(context.TODO(), db, w, articleID, tagNames)
}

func GetArticlesByTagCtx(ctx context.Context, db *sql.DB, tagName string, page, perPage int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	offset := (page - 1) * perPage

	var total int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM article_tags at
		 JOIN tags t ON t.id = at.tag_id
		 JOIN articles a ON a.id = at.article_id
		 WHERE t.name = ?`, tagName,
	).Scan(&total)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, NULL AS rendered_html, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a
		 LEFT JOIN users u ON u.id = a.author_id
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
		if err := scanArticleWithUser(rows, &a); err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tagMap, err := GetTagsForArticlesBatchCtx(ctx, db, articles)
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

// Deprecated: Use GetArticlesByTagCtx instead.
func GetArticlesByTag(db *sql.DB, tagName string, page, perPage int) (*ArticleListResult, error) {
	return GetArticlesByTagCtx(context.TODO(), db, tagName, page, perPage)
}
