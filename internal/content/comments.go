package content

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	maxCommentContentLen = 500
	maxLineCommentLen    = 20
)

var ErrCommentTooLong = errors.New("评论内容过长")

type Comment struct {
	ID             int       `json:"id"`
	ArticleID      int       `json:"article_id"`
	LineNumber     *int      `json:"line_number,omitempty"`
	UserID         *int      `json:"user_id,omitempty"`
	AuthorName     string    `json:"author_name"`
	AuthorNickname string    `json:"author_nickname,omitempty"`
	AuthorAvatar   string    `json:"author_avatar,omitempty"`
	Content        string    `json:"content"`
	ContentHTML    string    `json:"content_html,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

const commentSelect = `
SELECT c.id, c.article_id, c.line_number,
       COALESCE(c.user_id, 0) AS uid,
       c.author_name,
       COALESCE(u.nickname, '')  AS author_nickname,
       COALESCE(u.avatar, '')    AS author_avatar,
       c.content, c.created_at
FROM comments c
LEFT JOIN users u ON u.id = c.user_id`

func GetCommentsCtx(ctx context.Context, db *sql.DB, articleID int) ([]Comment, error) {
	rows, err := db.QueryContext(ctx,
		commentSelect+` WHERE c.article_id = ? AND c.line_number IS NULL ORDER BY c.created_at ASC`,
		articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

// Deprecated: Use GetCommentsCtx instead.
func GetComments(db *sql.DB, articleID int) ([]Comment, error) {
	return GetCommentsCtx(context.TODO(), db, articleID)
}

func GetLineCommentsCtx(ctx context.Context, db *sql.DB, articleID int) ([]Comment, error) {
	rows, err := db.QueryContext(ctx,
		commentSelect+` WHERE c.article_id = ? AND c.line_number IS NOT NULL ORDER BY c.line_number ASC, c.created_at ASC`,
		articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

// Deprecated: Use GetLineCommentsCtx instead.
func GetLineComments(db *sql.DB, articleID int) ([]Comment, error) {
	return GetLineCommentsCtx(context.TODO(), db, articleID)
}

func GetLineCommentCountsCtx(ctx context.Context, db *sql.DB, articleID int) (map[int]int, error) {
	rows, err := db.QueryContext(ctx,
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

// Deprecated: Use GetLineCommentCountsCtx instead.
func GetLineCommentCounts(db *sql.DB, articleID int) (map[int]int, error) {
	return GetLineCommentCountsCtx(context.TODO(), db, articleID)
}

func GetLineCommentsByLineCtx(ctx context.Context, db *sql.DB, articleID, lineNumber int) ([]Comment, error) {
	rows, err := db.QueryContext(ctx,
		commentSelect+` WHERE c.article_id = ? AND c.line_number = ? ORDER BY c.created_at ASC`,
		articleID, lineNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanComments(rows)
}

// Deprecated: Use GetLineCommentsByLineCtx instead.
func GetLineCommentsByLine(db *sql.DB, articleID, lineNumber int) ([]Comment, error) {
	return GetLineCommentsByLineCtx(context.TODO(), db, articleID, lineNumber)
}

func AddCommentCtx(ctx context.Context, db *sql.DB, articleID int, userID *int, authorName, content string) (*Comment, error) {
	if authorName == "" {
		authorName = "匿名"
	}
	if len([]rune(content)) > maxCommentContentLen {
		return nil, ErrCommentTooLong
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO comments (article_id, user_id, author_name, content) VALUES (?, ?, ?, ?)`,
		articleID, userID, authorName, content)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return getCommentByIDCtx(ctx, db, int(id))
}

// Deprecated: Use AddCommentCtx instead.
func AddComment(db *sql.DB, articleID int, userID *int, authorName, content string) (*Comment, error) {
	return AddCommentCtx(context.TODO(), db, articleID, userID, authorName, content)
}

func AddLineCommentCtx(ctx context.Context, db *sql.DB, articleID, lineNumber int, userID *int, authorName, content string) (*Comment, error) {
	if authorName == "" {
		authorName = "匿名"
	}
	if len([]rune(content)) > maxLineCommentLen {
		return nil, ErrCommentTooLong
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO comments (article_id, line_number, user_id, author_name, content) VALUES (?, ?, ?, ?, ?)`,
		articleID, lineNumber, userID, authorName, content)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return getCommentByIDCtx(ctx, db, int(id))
}

// Deprecated: Use AddLineCommentCtx instead.
func AddLineComment(db *sql.DB, articleID, lineNumber int, userID *int, authorName, content string) (*Comment, error) {
	return AddLineCommentCtx(context.TODO(), db, articleID, lineNumber, userID, authorName, content)
}

func DeleteCommentCtx(ctx context.Context, db *sql.DB, commentID int) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM comments WHERE id = ?`, commentID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Deprecated: Use DeleteCommentCtx instead.
func DeleteComment(db *sql.DB, commentID int) (bool, error) {
	return DeleteCommentCtx(context.TODO(), db, commentID)
}

func scanComments(rows *sql.Rows) ([]Comment, error) {
	var out = make([]Comment, 0)
	for rows.Next() {
		var c Comment
		var uid int
		var nickname, avatar sql.NullString
		if err := rows.Scan(
			&c.ID, &c.ArticleID, &c.LineNumber, &uid,
			&c.AuthorName, &nickname, &avatar,
			&c.Content, &c.CreatedAt,
		); err != nil {
			return nil, err
		}
		if uid != 0 {
			c.UserID = &uid
		}
		c.AuthorNickname = nickname.String
		c.AuthorAvatar = avatar.String
		out = append(out, c)
	}
	return out, rows.Err()
}

func getCommentByIDCtx(ctx context.Context, db *sql.DB, id int) (*Comment, error) {
	c := &Comment{}
	var uid int
	var nickname, avatar sql.NullString
	err := db.QueryRowContext(ctx,
		commentSelect+` WHERE c.id = ?`, id,
	).Scan(
		&c.ID, &c.ArticleID, &c.LineNumber, &uid,
		&c.AuthorName, &nickname, &avatar,
		&c.Content, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if uid != 0 {
		c.UserID = &uid
	}
	c.AuthorNickname = nickname.String
	c.AuthorAvatar = avatar.String
	return c, nil
}

func getCommentByID(db *sql.DB, id int) (*Comment, error) {
	return getCommentByIDCtx(context.TODO(), db, id)
}
