package api

import (
	"context"
	"database/sql"
	"strings"

	"gokych/internal/content/parsers"
)

type wikidotUserLookup struct {
	ctx context.Context
	db  *sql.DB
}

func (l *wikidotUserLookup) UserByName(name string) *parsers.UserProfile {
	if l == nil || l.db == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	ctx := l.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	row := l.db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar
		 FROM users
		 WHERE LOWER(username) = LOWER(?)
		 LIMIT 1`,
		name,
	)
	var (
		id       int64
		username sql.NullString
		nickname sql.NullString
		role     sql.NullString
		avatar   sql.NullString
	)
	if err := row.Scan(&id, &username, &nickname, &role, &avatar); err != nil {
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
