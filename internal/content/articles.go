package content

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"gokych/internal/typst"
)

type Article struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Content string `json:"content"`
	RenderedHTML string `json:"-"`
	AuthorID     *int   `json:"author_id"`
	AuthorName     string    `json:"author_name,omitempty"`
	AuthorNickname string    `json:"author_nickname,omitempty"`
	AuthorAvatar   string    `json:"author_avatar,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ArticleListResult struct {
	Articles   []Article `json:"articles"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalPages int       `json:"total_pages"`
	NextBefore int `json:"next_before,omitempty"`
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanArticleWithUser(s rowScanner, a *Article) error {
	var username, nickname, avatar sql.NullString
	var renderedHTML sql.NullString
	if err := s.Scan(
		&a.ID, &a.Type, &a.Slug, &a.Title, &a.Content, &renderedHTML, &a.AuthorID,
		&username, &nickname, &avatar, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return err
	}
	a.RenderedHTML = renderedHTML.String
	a.AuthorName = username.String
	a.AuthorNickname = nickname.String
	a.AuthorAvatar = avatar.String
	return nil
}

func scanArticlesWithTagsCtx(ctx context.Context, rows *sql.Rows, db *sql.DB) ([]Article, error) {
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
	return articles, nil
}

func scanArticlesWithTags(rows *sql.Rows, db *sql.DB) ([]Article, error) {
	return scanArticlesWithTagsCtx(context.TODO(), rows, db)
}

func buildListResult(articles []Article, total, page, perPage, nextBefore int) *ArticleListResult {
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
		NextBefore: nextBefore,
	}
}

func ListArticlesCtx(ctx context.Context, db *sql.DB, atype string, authorID *int, page, perPage, beforeID int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	if page < 1 {
		page = 1
	}

	var (
		whereSQL string
		args     []any
	)
	if atype != "" {
		whereSQL += "type = ?"
		args = append(args, atype)
	}
	if authorID != nil {
		if whereSQL != "" {
			whereSQL += " AND "
		}
		whereSQL += "author_id = ?"
		args = append(args, *authorID)
	}
	if whereSQL == "" {
		whereSQL = "1=1"
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM articles WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	listSQL := `SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, NULL AS rendered_html, a.author_id,
		            u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id WHERE ` + whereSQL
	listArgs := append([]any{}, args...)
	switch {
	case beforeID > 0:
		// Cursor pagination: caller passed `before=<last seen id>`, so we
		// take the next perPage items with id < that marker. Stable under
		// inserts/deletes (no offset drift if a row is added at the head).
		listSQL += " AND a.id < ?"
		listArgs = append(listArgs, beforeID, perPage)
		listSQL += " ORDER BY a.id DESC LIMIT ?"
	default:
		// Offset fallback: callers that don't track the cursor (the admin
		// "我的文章" / "文章管理" pager falls into this case, since the
		// client only sends `page`) get the standard LIMIT/OFFSET slice.
		// `page` is 1-indexed; page=1 ⇒ offset=0 which is the head of the
		// list, same as before with a no-cursor call.
		listSQL += " ORDER BY a.id DESC LIMIT ? OFFSET ?"
		listArgs = append(listArgs, perPage, (page-1)*perPage)
	}

	rows, err := db.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles, err := scanArticlesWithTagsCtx(ctx, rows, db)
	if err != nil {
		return nil, err
	}

	nextBefore := 0
	if n := len(articles); n >= perPage && n > 0 {
		nextBefore = articles[n-1].ID
	}

	return buildListResult(articles, total, page, perPage, nextBefore), nil
}

// Deprecated: Use ListArticlesCtx instead.
func ListArticles(db *sql.DB, atype string, authorID *int, page, perPage, beforeID int) (*ArticleListResult, error) {
	return ListArticlesCtx(context.TODO(), db, atype, authorID, page, perPage, beforeID)
}

func GetArticleCtx(ctx context.Context, db *sql.DB, atype, slug string) (*Article, error) {
	a := &Article{}
	row := db.QueryRowContext(ctx,
		`SELECT a.id, a.type, a.slug, a.title, a.content, a.rendered_html, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id
		 WHERE a.type = ? AND a.slug = ?`, atype, slug,
	)
	if err := scanArticleWithUser(row, a); err != nil {
		return nil, err
	}
	tags, err := GetTagsForArticleCtx(ctx, db, a.ID)
	if err != nil {
		return nil, err
	}
	a.Tags = tags
	return a, nil
}

// Deprecated: Use GetArticleCtx instead.
func GetArticle(db *sql.DB, atype, slug string) (*Article, error) {
	return GetArticleCtx(context.TODO(), db, atype, slug)
}

func CreateArticleCtx(ctx context.Context, db *sql.DB, w *typst.Worker, atype, slug, title, content string, authorID *int) (*Article, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO articles (type, slug, title, content, author_id) VALUES (?, ?, ?, ?, ?)`,
		atype, slug, title, content, authorID,
	); err != nil {
		return nil, err
	}

	// Look up the assigned id inside the same tx so the seq=1 revision
	// references a real article row even under concurrent inserts.
	var articleID int
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM articles WHERE type = ? AND slug = ?`, atype, slug,
	).Scan(&articleID); err != nil {
		return nil, err
	}

	// First revision is always a snapshot (ShouldSnapshot handles this
	// rule, but calling it explicitly documents intent at the write site).
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO article_revisions
		 (article_id, seq, title, patch, is_snapshot, parent_seq, author_id, message)
		 VALUES (?, 1, ?, ?, 1, NULL, ?, '')`,
		articleID, title, content, authorID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	a, err := GetArticleCtx(ctx, db, atype, slug)
	if err != nil {
		return nil, err
	}
	if atype != "typst" {
		if rerr := RenderAndSaveCtx(ctx, db, w, a); rerr != nil {
			slog.Warn("createArticle: render cache failed", "article_id", a.ID, "err", rerr)
		}
	} else {
		if w != nil {
			if qerr := w.EnqueueCompile(a.ID); qerr != nil {
				slog.Warn("failed to enqueue typst compile on create", "article_id", a.ID, "err", qerr)
			}
		}
	}
	return a, nil
}

// Deprecated: Use CreateArticleCtx instead.
func CreateArticle(db *sql.DB, w *typst.Worker, atype, slug, title, content string, authorID *int) (*Article, error) {
	return CreateArticleCtx(context.TODO(), db, w, atype, slug, title, content, authorID)
}

// UpdateArticleCtx saves a new revision of an article.
//
// Revision semantics
// ──────────────────
//   - If `content` is byte-identical to the stored `articles.content`,
//     we treat it as a title-only edit: the title is updated in place
//     and NO new article_revisions row is written. This keeps the
//     history lean — a rename shouldn't pollute the diff log. Title
//     changes are still discoverable via articles.updated_at.
//   - If `content` actually changed, we open a transaction:
//       1. SELECT ... FOR UPDATE on the article row (serialises
//          concurrent writers on the same article)
//       2. SELECT the latest revision to find last_seq
//       3. ComputePatch(old, new) → ShouldSnapshot decides storage form
//       4. INSERT into article_revisions
//       5. UPDATE articles
//       6. COMMIT
//     Cache invalidation and re-render happen AFTER commit (they use a
//     separate *sql.DB connection, which would deadlock if held inside
//     the tx).
//
// The `message` parameter is the optional commit message supplied by
// the caller (e.g. the API's `message` request body field). Empty is
// fine — the column is VARCHAR(500) DEFAULT '' and we don't reject
// empty messages at the content layer.
func UpdateArticleCtx(ctx context.Context, db *sql.DB, w *typst.Worker, atype, slug, title, contentStr, message string) (*Article, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Lock the article row for the duration of the tx so two concurrent
	// PUTs compute diffs against the same baseline. Without FOR UPDATE,
	// both writers would see the same `old` and produce conflicting
	// seq=N rows; one would 1062 on the UNIQUE (article_id, seq) index.
	var oldID int
	var oldContent string
	var oldAuthorID sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT id, content, author_id FROM articles WHERE type = ? AND slug = ? FOR UPDATE`,
		atype, slug,
	).Scan(&oldID, &oldContent, &oldAuthorID); err != nil {
		return nil, err
	}

	// Title-only fast path: don't pollute the revision log with renames.
	if oldContent == contentStr {
		if _, err := tx.ExecContext(ctx,
			`UPDATE articles SET title = ? WHERE id = ?`, title, oldID,
		); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		a, err := GetArticleCtx(ctx, db, atype, slug)
		if err != nil {
			return nil, err
		}
		// Title changes don't invalidate rendered_html — the rendered
		// output references content blocks, not the <h1> title.
		return a, nil
	}

	// Content actually changed → record a new revision. The shared
	// recordRevisionInTx helper handles seq assignment, diff vs
	// snapshot policy, and the INSERT — the same logic RestoreRevisionCtx
	// needs, so we don't duplicate it.
	var authorID *int
	if oldAuthorID.Valid {
		v := int(oldAuthorID.Int64)
		authorID = &v
	}

	if _, err := recordRevisionInTx(ctx, tx, oldID, oldContent, contentStr, title, message, authorID); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE articles SET title = ?, content = ? WHERE id = ?`,
		title, contentStr, oldID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	a, err := GetArticleCtx(ctx, db, atype, slug)
	if err != nil {
		return nil, err
	}
	invalidated := InvalidateCacheCascadingCtx(ctx, db, oldID)
	if atype != "typst" {
		if rerr := RenderAndSaveCtx(ctx, db, w, a); rerr != nil {
			slog.Warn("updateArticle: render cache failed", "article_id", a.ID, "err", rerr)
		}
		for _, did := range invalidated {
			if did == a.ID {
				continue
			}
			if derr := renderAndSaveByIDCtx(ctx, db, w, did); derr != nil {
				slog.Warn("updateArticle: dependent re-render failed", "dep_id", did, "err", derr)
			}
		}
	} else {
		if _, derr := db.ExecContext(ctx, `DELETE FROM typst_cache WHERE article_id = ?`, a.ID); derr != nil {
			slog.Warn("failed to invalidate typst_cache", "article_id", a.ID, "err", derr)
		}
		if w != nil {
			if qerr := w.EnqueueCompile(a.ID); qerr != nil {
				slog.Warn("failed to enqueue typst compile on update", "article_id", a.ID, "err", qerr)
			}
			if ierr := w.EnqueueDependents(a.ID); ierr != nil {
				slog.Warn("failed to enqueue typst dependents", "article_id", a.ID, "err", ierr)
			}
		}
	}
	return a, nil
}

// Deprecated: Use UpdateArticleCtx instead.
func UpdateArticle(db *sql.DB, w *typst.Worker, atype, slug, title, contentStr string) (*Article, error) {
	return UpdateArticleCtx(context.TODO(), db, w, atype, slug, title, contentStr, "")
}

// RestoreRevisionCtx rolls an article back to a historical seq, creating
// a NEW revision (seq = current_max + 1) that records the change. This
// is the "revert, not reset" semantics agreed in V0: no revision row is
// ever deleted, and the chain stays append-only. Auditors can see "at
// seq=K the article was at version N" without losing what was there
// in between.
//
// Behaviour
// ─────────
//  1. Open a tx with SELECT ... FOR UPDATE on the article row (same
//     concurrency guard as UpdateArticleCtx — two simultaneous
//     restores would race on the UNIQUE (article_id, seq) index
//     otherwise).
//  2. Load the full revision chain (read-only, via *sql.DB outside
//     the tx — restore is low-frequency, the snapshot isolation gives
//     us a coherent view, and a separate connection avoids holding
//     a tx open across the rebuild).
//  3. Validate targetSeq against len(revs) (1 ≤ targetSeq ≤ len(revs)).
//  4. RebuildToSeq(revs, targetSeq) → restoredContent + restoredTitle.
//  5. If restoredContent == current content, this is a no-op
//     restore (user clicked restore on the current version). We
//     commit and return the current article without writing a row
//     or invalidating caches — same fast-path reasoning as
//     UpdateArticleCtx's title-only branch.
//  6. Otherwise: recordRevisionInTx writes a new row with the
//     caller-provided (or default "restore from #N") message and
//     the same parent_seq handling as a normal save. Then we
//     UPDATE articles to the restored title + content.
//  7. Commit, then run the same cache-invalidate + re-render dance
//     as UpdateArticleCtx.
//
// `message` is optional — empty is allowed. When empty, the helper
// records "restore from #N" so the chain still has a self-explanatory
// label for the row.
func RestoreRevisionCtx(ctx context.Context, db *sql.DB, w *typst.Worker, atype, slug string, targetSeq int, message string) (*Article, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Same row lock as UpdateArticleCtx — the read-modify-write of
	// "find latest seq → write new seq=N+1" is not safe under
	// concurrent writers without a serialising lock on the article
	// row.
	var oldID int
	var oldContent string
	var oldAuthorID sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT id, content, author_id FROM articles WHERE type = ? AND slug = ? FOR UPDATE`,
		atype, slug,
	).Scan(&oldID, &oldContent, &oldAuthorID); err != nil {
		return nil, err
	}

	// Load the chain outside the tx (separate connection — see
	// step 2 in the doc above). A future optimisation could push
	// this inside the tx for stronger isolation, but restore is
	// rare and the current shape matches GetAllRevisionsForArticleCtx.
	revs, err := GetAllRevisionsForArticleCtx(ctx, db, oldID)
	if err != nil {
		return nil, err
	}
	if targetSeq < 1 || targetSeq > len(revs) {
		return nil, fmt.Errorf("restore: target seq %d out of range [1, %d]", targetSeq, len(revs))
	}

	restoredContent, err := RebuildToSeq(revs, targetSeq)
	if err != nil {
		return nil, fmt.Errorf("restore: rebuild seq=%d: %w", targetSeq, err)
	}
	restoredTitle := revs[targetSeq-1].Title

	// No-op fast path: if the requested seq is byte-identical to the
	// current state, we still return success (the user did ask
	// for this version to be current — it already is), but we
	// skip the revision row + cache invalidation. This also
	// happens if the user restores to a seq whose content is
	// unchanged (e.g. seq=2 and seq=3 had the same content because
	// the third save was a title-only change that we filtered out
	// from the chain in UpdateArticleCtx's fast path).
	if oldContent == restoredContent {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return GetArticleCtx(ctx, db, atype, slug)
	}

	var authorID *int
	if oldAuthorID.Valid {
		v := int(oldAuthorID.Int64)
		authorID = &v
	}

	// Default commit message for the revert row. Without this, the
	// row's message column would be empty and the audit trail
	// (the only visible signal that "seq=N was a restore") would
	// live only in the patch's content delta. Self-documenting
	// message wins over terseness.
	if message == "" {
		message = fmt.Sprintf("restore from #%d", targetSeq)
	}

	if _, err := recordRevisionInTx(ctx, tx, oldID, oldContent, restoredContent, restoredTitle, message, authorID); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE articles SET title = ?, content = ? WHERE id = ?`,
		restoredTitle, restoredContent, oldID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	a, err := GetArticleCtx(ctx, db, atype, slug)
	if err != nil {
		return nil, err
	}
	invalidated := InvalidateCacheCascadingCtx(ctx, db, oldID)
	if atype != "typst" {
		if rerr := RenderAndSaveCtx(ctx, db, w, a); rerr != nil {
			slog.Warn("restore: render cache failed", "article_id", a.ID, "err", rerr)
		}
		for _, did := range invalidated {
			if did == a.ID {
				continue
			}
			if derr := renderAndSaveByIDCtx(ctx, db, w, did); derr != nil {
				slog.Warn("restore: dependent re-render failed", "dep_id", did, "err", derr)
			}
		}
	} else {
		if _, derr := db.ExecContext(ctx, `DELETE FROM typst_cache WHERE article_id = ?`, a.ID); derr != nil {
			slog.Warn("restore: invalidate typst_cache", "article_id", a.ID, "err", derr)
		}
		if w != nil {
			if qerr := w.EnqueueCompile(a.ID); qerr != nil {
				slog.Warn("restore: enqueue typst compile", "article_id", a.ID, "err", qerr)
			}
			if ierr := w.EnqueueDependents(a.ID); ierr != nil {
				slog.Warn("restore: enqueue typst dependents", "article_id", a.ID, "err", ierr)
			}
		}
	}
	return a, nil
}

func renderAndSaveByIDCtx(ctx context.Context, db *sql.DB, w *typst.Worker, id int) error {
	var atype, slug string
	err := db.QueryRowContext(ctx, `SELECT type, slug FROM articles WHERE id = ?`, id).Scan(&atype, &slug)
	if err != nil {
		return err
	}
	a, err := GetArticleCtx(ctx, db, atype, slug)
	if err != nil {
		return err
	}
	return RenderAndSaveCtx(ctx, db, w, a)
}

func renderAndSaveByID(db *sql.DB, w *typst.Worker, id int) error {
	return renderAndSaveByIDCtx(context.TODO(), db, w, id)
}

func InvalidateArticleCacheCtx(ctx context.Context, db *sql.DB, w *typst.Worker, articleID int) {
	invalidated := InvalidateCacheCascadingCtx(ctx, db, articleID)
	for _, did := range invalidated {
		var atype string
		if err := db.QueryRowContext(ctx, `SELECT type FROM articles WHERE id = ?`, did).Scan(&atype); err != nil {
			continue
		}
		if atype == "typst" {
			_, _ = db.ExecContext(ctx, `DELETE FROM typst_cache WHERE article_id = ?`, did)
			if w != nil {
				_ = w.EnqueueCompile(did)
			}
		} else {
			if err := renderAndSaveByIDCtx(ctx, db, w, did); err != nil {
				slog.Warn("InvalidateArticleCache: re-render failed", "article_id", did, "err", err)
			}
		}
	}
}

// Deprecated: Use InvalidateArticleCacheCtx instead.
func InvalidateArticleCache(db *sql.DB, w *typst.Worker, articleID int) {
	InvalidateArticleCacheCtx(context.TODO(), db, w, articleID)
}

func DeleteArticleCtx(ctx context.Context, db *sql.DB, w *typst.Worker, atype, slug string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var articleID int
	row := tx.QueryRowContext(ctx, `SELECT id FROM articles WHERE type = ? AND slug = ?`, atype, slug)
	if err := row.Scan(&articleID); err != nil {
		return false, nil
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM articles WHERE id = ?`, articleID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, err
	}

	if n > 0 {
		invalidated := InvalidateCacheCascadingCtx(ctx, db, articleID)
		if atype == "typst" && w != nil {
			if ierr := w.EnqueueDependents(articleID); ierr != nil {
				slog.Warn("failed to enqueue typst dependents on delete", "article_id", articleID, "err", ierr)
			}
		}
		for _, did := range invalidated {
			if did == articleID {
				continue
			}
			var dtype string
			if err := db.QueryRowContext(ctx, `SELECT type FROM articles WHERE id = ?`, did).Scan(&dtype); err == nil && dtype != "typst" {
				if derr := renderAndSaveByIDCtx(ctx, db, w, did); derr != nil {
					slog.Warn("deleteArticle: dependent re-render failed", "dep_id", did, "err", derr)
				}
			}
		}
	}
	return n > 0, nil
}

// Deprecated: Use DeleteArticleCtx instead.
func DeleteArticle(db *sql.DB, w *typst.Worker, atype, slug string) (bool, error) {
	return DeleteArticleCtx(context.TODO(), db, w, atype, slug)
}

func ListRecentArticlesCtx(ctx context.Context, db *sql.DB, limit int) ([]Article, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, NULL AS rendered_html, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id
		 ORDER BY a.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticlesWithTagsCtx(ctx, rows, db)
}

// Deprecated: Use ListRecentArticlesCtx instead.
func ListRecentArticles(db *sql.DB, limit int) ([]Article, error) {
	return ListRecentArticlesCtx(context.TODO(), db, limit)
}

func SearchArticlesCtx(ctx context.Context, db *sql.DB, q string, page, perPage int) (*ArticleListResult, error) {
	if perPage <= 0 {
		perPage = 10
	}
	keyword := strings.TrimSpace(q)
	offset := (page - 1) * perPage
	if keyword == "" {
		return buildListResult(make([]Article, 0), 0, page, perPage, 0), nil
	}

	var total int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM articles WHERE MATCH(title, content) AGAINST(?)`,
		keyword,
	).Scan(&total)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT a.id, a.type, a.slug, a.title, LEFT(a.content, 200) AS content, NULL AS rendered_html, a.author_id,
		        u.username, u.nickname, u.avatar, a.created_at, a.updated_at
		 FROM articles a LEFT JOIN users u ON u.id = a.author_id
		 WHERE MATCH(a.title, a.content) AGAINST(?)
		 ORDER BY MATCH(a.title, a.content) AGAINST(?) DESC, a.updated_at DESC LIMIT ? OFFSET ?`,
		keyword, keyword, perPage, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles, err := scanArticlesWithTagsCtx(ctx, rows, db)
	if err != nil {
		return nil, err
	}
	return buildListResult(articles, total, page, perPage, 0), nil
}

// Deprecated: Use SearchArticlesCtx instead.
func SearchArticles(db *sql.DB, q string, page, perPage int) (*ArticleListResult, error) {
	return SearchArticlesCtx(context.TODO(), db, q, page, perPage)
}
