package api

import (
	"database/sql"
	"strings"

	"gokych/internal/content"
	"gokych/internal/content/parsers"
)

// wikidotPageLookup is the production adapter that satisfies
// parsers.PageLookup for the wikidot renderer's dynamic
// constructs (`[[include …]]`, `[[module ListPages]]`,
// `[[module RandomPage]]`). It translates the parser's
// high-level queries (resolve slug, list by category, pick
// random) into MySQL queries against the articles table.
//
// The adapter intentionally takes the loaded Article for
// the *current* render (so we can skip the recursive-include
// case where the target IS the article being rendered) and
// for the per-article context (vars injection, current user).
// That context lives on the Server / per-request handler —
// the PageLookup gets a fresh adapter instance per render so
// concurrent requests don't share state.
type wikidotPageLookup struct {
	db            *sql.DB
	currentType   string
	currentSlug   string
	currentUserID *int
}

// IncludeBySlug resolves `[[include type:slug]]` (or
// `[[include slug]]` using the current article's type) to
// the article's raw content for recursive rendering. Returns
// nil for an unknown slug (caller falls back to raw source
// so the author can see and fix the link).
func (l *wikidotPageLookup) IncludeBySlug(atype, slug string) *parsers.IncludedPage {
	if l == nil || l.db == nil {
		return nil
	}
	// Strip a leading `category:` namespace from the slug —
	// Wikidot's `[[include category:page]]` form is the
	// canonical include syntax. Our articles table stores
	// slugs without the namespace, so we look up by slug
	// alone.
	slug = strings.TrimSpace(slug)
	// Don't recursively include the same article that's
	// being rendered (would loop forever).
	if atype == l.currentType && slug == l.currentSlug {
		return nil
	}
	a, err := content.GetArticle(l.db, atype, slug)
	if err != nil {
		return nil
	}
	return &parsers.IncludedPage{
		Type:    a.Type,
		Content: a.Content,
		Title:   a.Title,
	}
}

// ListPages returns the rows that match the module's
// category / limit / order. The current implementation is
// narrowly scoped to a single wikidot-style module call:
//   - category is interpreted as a literal match against the
//     `articles.type` column when it's a known type
//     ("wikidot" / "md" / etc.). "*" means "any type".
//   - limit is the SQL LIMIT (capped at 100).
//   - order is one of the small whitelist
//     ("created_at desc", "title", "updated_at desc"). The
//     parser already filters to safe values; we double-check
//     here so a future caller can't inject SQL via `order`.
//
// `%%title%%` / `%%slug%%` / `%%author_name%%` /
// `%%created_at%%` / `%%tags%%` / `%%rating%%` are all
// populated from the row + the tags/ratings sub-queries.
func (l *wikidotPageLookup) ListPages(category string, limit int, order string) []parsers.ListPageEntry {
	if l == nil || l.db == nil {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	// Whitelist the order-by clause. Anything not on the
	// list falls back to created_at desc, which is the
	// "newest pages first" default Wikidot uses.
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
	// Whitelist the category filter. "*" (or empty) means
	// "no filter"; otherwise we constrain by type.
	var (
		whereClause string
		args        []any
	)
	if category != "" && category != "*" {
		// Allow only known article types. Authors who want
		// to filter by something more specific should add
		// a category column to articles later; for now,
		// the type column is the only stable filter.
		// Normalize aliases: "markdown" -> "md" to match database storage.
		dbType := category
		switch category {
		case "markdown":
			dbType = "md"
		case "wikidot", "md", "bbcode", "html", "typst":
			// already valid
		default:
			// Unknown filter — treat as "no match" rather
			// than risk a SQL error.
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
	rows, err := l.db.Query(q, args...)
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
	return out
}

// RandomPage returns a single random article matching the
// category filter. "Any" (category="*") is the common case.
// Returns nil for an empty result set.
//
// Implementation: a single `ORDER BY RAND() LIMIT 1` query.
// ORDER BY RAND() on a small personal-wiki-sized table is
// fine; for a high-traffic install we'd swap in a TABLESAMPLE
// or pre-computed random-id lookup.
func (l *wikidotPageLookup) RandomPage(category string) *parsers.ListPageEntry {
	if l == nil || l.db == nil {
		return nil
	}
	var (
		whereClause string
		args        []any
	)
	if category != "" && category != "*" {
		// Normalize aliases: "markdown" -> "md" to match database storage
		dbType := category
		switch category {
		case "markdown":
			dbType = "md"
		case "wikidot", "md", "bbcode", "html", "typst":
			// already valid
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
	var e parsers.ListPageEntry
	var username, nickname sql.NullString
	err := l.db.QueryRow(q, args...).Scan(&e.Slug, &e.Title, &username, &nickname, &e.CreatedAt)
	if err != nil {
		return nil
	}
	e.AuthorName = username.String
	e.AuthorNickname = nickname.String
	return &e
}
