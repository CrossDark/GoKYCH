package api

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

type wikidotPageLookup struct {
	ctx           context.Context
	db            *sql.DB
	currentType   string
	currentSlug   string
	currentUserID *int
}

func (l *wikidotPageLookup) IncludeBySlug(atype, slug string) *parsers.IncludedPage {
	if l == nil || l.db == nil {
		return nil
	}
	slug = strings.TrimSpace(slug)
	if atype == l.currentType && slug == l.currentSlug {
		return nil
	}
	ctx := l.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a, err := content.GetArticleCtx(ctx, l.db, atype, slug)
	if err != nil {
		return nil
	}
	return &parsers.IncludedPage{
		Type:    a.Type,
		Content: a.Content,
		Title:   a.Title,
	}
}

func (l *wikidotPageLookup) ListPages(category string, limit int, order string) []parsers.ListPageEntry {
	if l == nil || l.db == nil {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	orderBy := "a.created_at DESC"
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "title", "title asc":
		orderBy = "a.title ASC"
	case "title desc":
		orderBy = "a.title DESC"
	case "updated_at desc":
		orderBy = "a.updated_at DESC"
	case "created_at", "created_at asc", "":
		orderBy = "a.created_at ASC"
	}
	var (
		whereClause string
		args        []any
	)
	if category != "" && category != "*" {
		dbType := category
		switch category {
		case "markdown":
			dbType = "md"
		case "wikidot", "md", "bbcode", "html", "typst":
		default:
			return nil
		}
		whereClause = "WHERE a.type = ?"
		args = append(args, dbType)
	}
	q := `SELECT a.slug, a.title, u.username, u.nickname, a.created_at
	      FROM articles a LEFT JOIN users u ON u.id = a.author_id
	      ` + whereClause + `
	      ORDER BY ` + orderBy + ` LIMIT ?`
	args = append(args, limit)
	ctx := l.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]parsers.ListPageEntry, 0, limit)
	for rows.Next() {
		var e parsers.ListPageEntry
		var username, nickname sql.NullString
		if err := rows.Scan(&e.Slug, &e.Title, &username, &nickname, &e.CreatedAt); err != nil {
			continue
		}
		e.AuthorName = username.String
		e.AuthorNickname = nickname.String
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		slog.Error("wikidot ListPages: iterate rows", "err", err)
	}
	return out
}

func (l *wikidotPageLookup) RandomPage(category string) *parsers.ListPageEntry {
	if l == nil || l.db == nil {
		return nil
	}
	var (
		whereClause string
		args        []any
	)
	if category != "" && category != "*" {
		dbType := category
		switch category {
		case "markdown":
			dbType = "md"
		case "wikidot", "md", "bbcode", "html", "typst":
		default:
			return nil
		}
		whereClause = "WHERE a.type = ?"
		args = append(args, dbType)
	}
	q := `SELECT a.slug, a.title, u.username, u.nickname, a.created_at
	      FROM articles a LEFT JOIN users u ON u.id = a.author_id
	      ` + whereClause + `
	      ORDER BY RAND() LIMIT 1`
	ctx := l.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var e parsers.ListPageEntry
	var username, nickname sql.NullString
	err := l.db.QueryRowContext(ctx, q, args...).Scan(&e.Slug, &e.Title, &username, &nickname, &e.CreatedAt)
	if err != nil {
		return nil
	}
	e.AuthorName = username.String
	e.AuthorNickname = nickname.String
	return &e
}
