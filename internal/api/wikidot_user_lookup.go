package api

import (
	"database/sql"
	"strings"

	"gokych/internal/content/parsers"
)

// wikidotUserLookup is the production adapter that satisfies
// parsers.UserLookup for the wikidot renderer's user-mention
// resolution (`[[user Name]]` / `[[*user Name]]`). It translates
// the parser's high-level "what's the profile for this typed
// name?" query into a single MySQL lookup against the users
// table.
//
// The adapter holds no state beyond the DB handle; the
// PageLookup-equivalent constructor pattern (per-render
// instance, not a package-global) is used so the parser can
// pool its own instances without sharing DB connections
// across concurrent renders.
type wikidotUserLookup struct {
	db *sql.DB
}

// UserByName resolves a typed `[[user Name]]` mention to the
// matching user row. Lookup is case-insensitive on the canonical
// `username` column (Wikidot users type their name however they
// want; the DB stores it as-typed but we accept any casing on
// the way in). Returns nil for:
//
//   - unknown name (so the renderer can fall back to the
//     pre-lookup "@Name" link with no avatar / nickname enrichment)
//
//   - malformed input (empty name, name with whitespace only)
//
//   - DB error (the renderer degrades gracefully — better to
//     show a plain link than to drop the mention entirely on a
//     transient DB blip)
//
// `IsStaff` is derived from the `role` column: both `admin` and
// `owner` are considered staff (the front-end badge / class is
// the same for both). The `user` role is non-staff.
func (l *wikidotUserLookup) UserByName(name string) *parsers.UserProfile {
	if l == nil || l.db == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	// Case-insensitive match on `username` (the canonical
	// login name). The `nickname` column is NOT consulted
	// here — authors who mention someone by their nickname
	// rather than their login will get the lookup miss
	// path and see their own typed text, which is the
	// safer default (nicknames can collide).
	row := l.db.QueryRow(
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
		// sql.ErrNoRows is the common case (unknown name);
		// a real DB error (network / auth) also returns
		// nil so the renderer falls back cleanly.
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
