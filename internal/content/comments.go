package content

import (
	"database/sql"
	"time"
)

// Comment represents a full-text or line comment.
type Comment struct {
	ID         int       `json:"id"`
	ArticleID  int       `json:"article_id"`
	LineNumber *int      `json:"line_number,omitempty"`
	AuthorName string    `json:"author_name"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// GetComments returns full-text comments (line_number IS NULL) for an article.
func GetComments(db *sql.DB, articleID int) ([]Comment, error) {
	rows, err := db.Query(
		`SELECT id, article_id, line_number, author_name, content, created_at
		 FROM comments WHERE article_id = ? AND line_number IS NULL
		 ORDER BY created_at ASC`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

// GetLineComments returns line comments for an article, ordered by line then time.
func GetLineComments(db *sql.DB, articleID int) ([]Comment, error) {
	rows, err := db.Query(
		`SELECT id, article_id, line_number, author_name, content, created_at
		 FROM comments WHERE article_id = ? AND line_number IS NOT NULL
		 ORDER BY line_number ASC, created_at ASC`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

// GetLineCommentCounts returns a map of line_number → count.
func GetLineCommentCounts(db *sql.DB, articleID int) (map[int]int, error) {
	rows, err := db.Query(
		`SELECT line_number, COUNT(*) FROM comments
		 WHERE article_id = ? AND line_number IS NOT NULL
		 GROUP BY line_number`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]int)
	for rows.Next() {
		var ln, cnt int
		if err := rows.Scan(&ln, &cnt); err != nil {
			return nil, err
		}
		out[ln] = cnt
	}
	return out, rows.Err()
}

// GetLineCommentsByLine returns comments for a specific line.
func GetLineCommentsByLine(db *sql.DB, articleID, lineNumber int) ([]Comment, error) {
	rows, err := db.Query(
		`SELECT id, article_id, line_number, author_name, content, created_at
		 FROM comments WHERE article_id = ? AND line_number = ?
		 ORDER BY created_at ASC`, articleID, lineNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

// AddComment inserts a full-text comment.
func AddComment(db *sql.DB, articleID int, authorName, content string) (*Comment, error) {
	if authorName == "" {
		authorName = "匿名"
	}
	res, err := db.Exec(
		`INSERT INTO comments (article_id, author_name, content) VALUES (?, ?, ?)`,
		articleID, authorName, content)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return getCommentByID(db, int(id))
}

// AddLineComment inserts a line comment (capped at 20 chars).
func AddLineComment(db *sql.DB, articleID, lineNumber int, authorName, content string) (*Comment, error) {
	if authorName == "" {
		authorName = "匿名"
	}
	// Cap at 20 chars.
	if len(content) > 20 {
		content = content[:20]
	}
	res, err := db.Exec(
		`INSERT INTO comments (article_id, line_number, author_name, content) VALUES (?, ?, ?, ?)`,
		articleID, lineNumber, authorName, content)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return getCommentByID(db, int(id))
}

// DeleteComment removes a comment by ID.
func DeleteComment(db *sql.DB, commentID int) (bool, error) {
	res, err := db.Exec(`DELETE FROM comments WHERE id = ?`, commentID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ── helpers ──────────────────────────────────────────────────────────

func scanComments(rows *sql.Rows) ([]Comment, error) {
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.ArticleID, &c.LineNumber, &c.AuthorName, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func getCommentByID(db *sql.DB, id int) (*Comment, error) {
	c := &Comment{}
	err := db.QueryRow(
		`SELECT id, article_id, line_number, author_name, content, created_at
		 FROM comments WHERE id = ?`, id,
	).Scan(&c.ID, &c.ArticleID, &c.LineNumber, &c.AuthorName, &c.Content, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}
