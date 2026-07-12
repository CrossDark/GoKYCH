package content

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var discussionSlugRe = regexp.MustCompile(`[\x00-\x1F\x7F/\\]`)
const maxDiscussionSlugRunes = 128

type Discussion struct {
	ID          int       `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	ContentHTML string    `json:"content_html,omitempty"`
	Format      string    `json:"format"`
	AuthorID    *int      `json:"author_id,omitempty"`
	AuthorName  string    `json:"author_name,omitempty"`
	AuthorNickname string `json:"author_nickname,omitempty"`
	AuthorAvatar string   `json:"author_avatar,omitempty"`
	ReplyCount  int       `json:"reply_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DiscussionReply struct {
	ID           int       `json:"id"`
	DiscussionID int       `json:"discussion_id"`
	Content      string    `json:"content"`
	ContentHTML  string    `json:"content_html,omitempty"`
	Format       string    `json:"format"`
	UserID       *int      `json:"user_id,omitempty"`
	AuthorName   string    `json:"author_name"`
	AuthorNickname string  `json:"author_nickname,omitempty"`
	AuthorAvatar string    `json:"author_avatar,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

const discussionSelect = `
SELECT d.id, d.slug, d.title, d.content, d.format,
       COALESCE(d.author_id, 0) AS uid,
       COALESCE(u.username, '') AS author_name,
       COALESCE(u.nickname, '')  AS author_nickname,
       COALESCE(u.avatar, '')    AS author_avatar,
       d.reply_count, d.created_at, d.updated_at
FROM discussions d
LEFT JOIN users u ON u.id = d.author_id`

func ListDiscussionsCtx(ctx context.Context, db *sql.DB, page, pageSize int, authorID *int) ([]Discussion, error) {
	offset := (page - 1) * pageSize
	var query string
	var args []interface{}
	if authorID != nil {
		query = discussionSelect + ` WHERE d.author_id = ? ORDER BY d.created_at DESC LIMIT ? OFFSET ?`
		args = []interface{}{*authorID, pageSize, offset}
	} else {
		query = discussionSelect + ` ORDER BY d.created_at DESC LIMIT ? OFFSET ?`
		args = []interface{}{pageSize, offset}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscussions(rows)
}

func CountDiscussionsCtx(ctx context.Context, db *sql.DB, authorID *int) (int, error) {
	var count int
	var query string
	var args []interface{}
	if authorID != nil {
		query = `SELECT COUNT(*) FROM discussions WHERE author_id = ?`
		args = []interface{}{*authorID}
	} else {
		query = `SELECT COUNT(*) FROM discussions`
	}
	err := db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func GetDiscussionCtx(ctx context.Context, db *sql.DB, id int) (*Discussion, error) {
	var d Discussion
	var uid int
	var authorName, nickname, avatar sql.NullString
	err := db.QueryRowContext(ctx,
		discussionSelect+` WHERE d.id = ?`, id,
	).Scan(
		&d.ID, &d.Slug, &d.Title, &d.Content, &d.Format,
		&uid, &authorName, &nickname, &avatar,
		&d.ReplyCount, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if uid != 0 {
		d.AuthorID = &uid
	}
	d.AuthorName = authorName.String
	d.AuthorNickname = nickname.String
	d.AuthorAvatar = avatar.String
	return &d, nil
}

func GetDiscussionBySlugCtx(ctx context.Context, db *sql.DB, slug string) (*Discussion, error) {
	var d Discussion
	var uid int
	var authorName, nickname, avatar sql.NullString
	err := db.QueryRowContext(ctx,
		discussionSelect+` WHERE d.slug = ?`, slug,
	).Scan(
		&d.ID, &d.Slug, &d.Title, &d.Content, &d.Format,
		&uid, &authorName, &nickname, &avatar,
		&d.ReplyCount, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if uid != 0 {
		d.AuthorID = &uid
	}
	d.AuthorName = authorName.String
	d.AuthorNickname = nickname.String
	d.AuthorAvatar = avatar.String
	return &d, nil
}

func generateDiscussionSlug(title string) string {
	slug := strings.TrimSpace(title)
	slug = discussionSlugRe.ReplaceAllString(slug, "")
	if len([]rune(slug)) > maxDiscussionSlugRunes {
		slug = string([]rune(slug)[:maxDiscussionSlugRunes])
	}
	return strings.TrimSpace(slug)
}

func CreateDiscussionCtx(ctx context.Context, db *sql.DB, title, content, format string, authorID *int) (*Discussion, error) {
	slug := generateDiscussionSlug(title)
	if slug == "" {
		slug = fmt.Sprintf("discussion-%d", time.Now().Unix())
	}

	var existingID int
	err := db.QueryRowContext(ctx, `SELECT id FROM discussions WHERE slug = ?`, slug).Scan(&existingID)
	if err == nil {
		slug = fmt.Sprintf("%s-%d", slug, time.Now().Unix())
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO discussions (slug, title, content, format, author_id) VALUES (?, ?, ?, ?, ?)`,
		slug, title, content, format, authorID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return GetDiscussionCtx(ctx, db, int(id))
}

func DeleteDiscussionCtx(ctx context.Context, db *sql.DB, id int) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM discussions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func scanDiscussions(rows *sql.Rows) ([]Discussion, error) {
	var out = make([]Discussion, 0)
	for rows.Next() {
		var d Discussion
		var uid int
		var authorName, nickname, avatar sql.NullString
		if err := rows.Scan(
			&d.ID, &d.Slug, &d.Title, &d.Content, &d.Format,
			&uid, &authorName, &nickname, &avatar,
			&d.ReplyCount, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if uid != 0 {
			d.AuthorID = &uid
		}
		d.AuthorName = authorName.String
		d.AuthorNickname = nickname.String
		d.AuthorAvatar = avatar.String
		out = append(out, d)
	}
	return out, rows.Err()
}

const replySelect = `
SELECT dr.id, dr.discussion_id, dr.content, dr.format,
       COALESCE(dr.user_id, 0) AS uid,
       dr.author_name,
       COALESCE(u.nickname, '')  AS author_nickname,
       COALESCE(u.avatar, '')    AS author_avatar,
       dr.created_at
FROM discussion_replies dr
LEFT JOIN users u ON u.id = dr.user_id`

func GetDiscussionRepliesCtx(ctx context.Context, db *sql.DB, discussionID int) ([]DiscussionReply, error) {
	rows, err := db.QueryContext(ctx,
		replySelect+` WHERE dr.discussion_id = ? ORDER BY dr.created_at ASC`,
		discussionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDiscussionReplies(rows)
}

func AddDiscussionReplyCtx(ctx context.Context, db *sql.DB, discussionID int, content, format string, userID *int, authorName string) (*DiscussionReply, error) {
	if authorName == "" {
		authorName = "匿名"
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO discussion_replies (discussion_id, content, format, user_id, author_name) VALUES (?, ?, ?, ?, ?)`,
		discussionID, content, format, userID, authorName)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()

	_, _ = db.ExecContext(ctx,
		`UPDATE discussions SET reply_count = reply_count + 1 WHERE id = ?`,
		discussionID)

	return getDiscussionReplyByIDCtx(ctx, db, int(id))
}

func getDiscussionReplyByIDCtx(ctx context.Context, db *sql.DB, id int) (*DiscussionReply, error) {
	var r DiscussionReply
	var uid int
	var nickname, avatar sql.NullString
	err := db.QueryRowContext(ctx,
		replySelect+` WHERE dr.id = ?`, id,
	).Scan(
		&r.ID, &r.DiscussionID, &r.Content, &r.Format,
		&uid, &r.AuthorName, &nickname, &avatar,
		&r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if uid != 0 {
		r.UserID = &uid
	}
	r.AuthorNickname = nickname.String
	r.AuthorAvatar = avatar.String
	return &r, nil
}

func scanDiscussionReplies(rows *sql.Rows) ([]DiscussionReply, error) {
	var out = make([]DiscussionReply, 0)
	for rows.Next() {
		var r DiscussionReply
		var uid int
		var nickname, avatar sql.NullString
		if err := rows.Scan(
			&r.ID, &r.DiscussionID, &r.Content, &r.Format,
			&uid, &r.AuthorName, &nickname, &avatar,
			&r.CreatedAt,
		); err != nil {
			return nil, err
		}
		if uid != 0 {
			r.UserID = &uid
		}
		r.AuthorNickname = nickname.String
		r.AuthorAvatar = avatar.String
		out = append(out, r)
	}
	return out, rows.Err()
}
