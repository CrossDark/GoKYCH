package typst

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	maxQueueAttempts    = 3
	pollInterval        = 2 * time.Second
	staleCompileTimeout = 5 * time.Minute
)

var (
	workerOnce   sync.Once
	workerWg     sync.WaitGroup
	workerCtx    context.Context
	workerCancel context.CancelFunc
)

// CompileStatus represents the async compilation status for a typst article.
type CompileStatus struct {
	ArticleID    int        `json:"article_id"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"error_message,omitempty"`
	Attempts     int        `json:"attempts"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompiledAt   *time.Time `json:"compiled_at,omitempty"`
}

// EnqueueCompile adds a typst article to the async compilation queue.
// If the article is already in the queue, it resets the status to 'pending'
// and clears any previous error (for re-compilation after edit).
// Non-blocking: returns immediately after the queue row is upserted.
func EnqueueCompile(dbx *sql.DB, articleID int) error {
	if dbx == nil {
		return errors.New("typst: db not configured")
	}
	if articleID <= 0 {
		return errors.New("typst: invalid article id")
	}
	if !Available() {
		_, err := dbx.Exec(
			`INSERT INTO typst_compile_queue (article_id, status, error_message, attempts)
			 VALUES (?, 'failed', ?, 0)
			 ON DUPLICATE KEY UPDATE
			   status = 'failed',
			   error_message = VALUES(error_message),
			   attempts = 0,
			   compiled_at = NULL`,
			articleID, "typst CLI not found — please install typst to compile articles",
		)
		if err != nil {
			return fmt.Errorf("typst: enqueue failed: %w", err)
		}
		slog.Warn("typst: enqueued with failure (CLI not found)", "article_id", articleID)
		return nil
	}
	_, err := dbx.Exec(
		`INSERT INTO typst_compile_queue (article_id, status, error_message, attempts, compiled_at)
		 VALUES (?, 'pending', NULL, 0, NULL)
		 ON DUPLICATE KEY UPDATE
		   status = 'pending',
		   error_message = NULL,
		   compiled_at = NULL`,
		articleID,
	)
	if err != nil {
		return fmt.Errorf("typst: enqueue failed: %w", err)
	}
	slog.Info("typst: enqueued for async compile", "article_id", articleID)
	return nil
}

// EnqueueDependents queues all articles that depend (via @import) on changedID
// for re-compilation. Used after UpdateArticle/DeleteArticle to keep caches fresh.
func EnqueueDependents(dbx *sql.DB, changedID int) error {
	if dbx == nil || changedID <= 0 {
		return nil
	}
	rows, err := dbx.Query(`SELECT article_id, dependencies FROM typst_cache WHERE dependencies IS NOT NULL AND dependencies != ''`)
	if err != nil {
		return fmt.Errorf("typst: query dependencies for enqueue: %w", err)
	}
	reverseDep := make(map[int][]int)
	for rows.Next() {
		var aid int
		var depsStr string
		if err := rows.Scan(&aid, &depsStr); err != nil {
			rows.Close()
			return err
		}
		for _, part := range strings.Split(depsStr, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			var did int
			if _, err := fmt.Sscanf(part, "%d", &did); err == nil && did > 0 {
				reverseDep[did] = append(reverseDep[did], aid)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("typst: iterate dependency rows: %w", err)
	}

	// BFS from changedID to find all transitively dependent articles.
	// Start BFS by enqueuing articles that DIRECTLY depend on changedID,
	// not changedID itself (the changed article is already being handled
	// by the caller and must not be re-queued here — that would cause an
	// infinite loop with circular @imports).
	visited := make(map[int]bool)
	var queue []int
	var toEnqueue []int
	for _, dep := range reverseDep[changedID] {
		if !visited[dep] {
			visited[dep] = true
			toEnqueue = append(toEnqueue, dep)
			queue = append(queue, dep)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range reverseDep[cur] {
			if !visited[dep] && dep != changedID {
				visited[dep] = true
				toEnqueue = append(toEnqueue, dep)
				queue = append(queue, dep)
			}
		}
	}

	for _, aid := range toEnqueue {
		if err := EnqueueCompile(dbx, aid); err != nil {
			slog.Warn("typst: failed to enqueue dependent", "article_id", aid, "err", err)
		}
	}

	// Also invalidate old caches for dependents so readers see the placeholder
	// instead of stale HTML.
	if len(toEnqueue) > 0 {
		if err := InvalidateDependents(dbx, changedID); err != nil {
			slog.Warn("typst: failed to invalidate dependent caches", "err", err)
		}
	}

	return nil
}

// GetCompileStatus returns the current compilation status for an article.
// Returns nil if no queue entry exists (meaning it was either never queued
// or compiled successfully before the queue was introduced — treat as "ready"
// if typst_cache has the content).
func GetCompileStatus(dbx *sql.DB, articleID int) (*CompileStatus, error) {
	if dbx == nil || articleID <= 0 {
		return nil, nil
	}
	var s CompileStatus
	var errMsg sql.NullString
	var compiledAt sql.NullTime
	err := dbx.QueryRow(
		`SELECT article_id, status, error_message, attempts, created_at, updated_at, compiled_at
		 FROM typst_compile_queue WHERE article_id = ?`,
		articleID,
	).Scan(&s.ArticleID, &s.Status, &errMsg, &s.Attempts, &s.CreatedAt, &s.UpdatedAt, &compiledAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	s.ErrorMessage = errMsg.String
	if compiledAt.Valid {
		s.CompiledAt = &compiledAt.Time
	}
	return &s, nil
}

// GetPendingCompileStatuses returns statuses for a batch of article IDs.
// Articles with no queue entry or with 'success' status are omitted.
func GetPendingCompileStatuses(dbx *sql.DB, articleIDs []int) (map[int]*CompileStatus, error) {
	if dbx == nil || len(articleIDs) == 0 {
		return nil, nil
	}
	result := make(map[int]*CompileStatus)
	placeholders := make([]string, len(articleIDs))
	args := make([]any, len(articleIDs))
	for i, id := range articleIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT article_id, status, error_message, attempts, created_at, updated_at, compiled_at
	      FROM typst_compile_queue WHERE article_id IN (` + strings.Join(placeholders, ",") + `)
	      AND status IN ('pending','compiling','failed')`
	rows, err := dbx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s CompileStatus
		var errMsg sql.NullString
		var compiledAt sql.NullTime
		if err := rows.Scan(&s.ArticleID, &s.Status, &errMsg, &s.Attempts, &s.CreatedAt, &s.UpdatedAt, &compiledAt); err != nil {
			return nil, err
		}
		s.ErrorMessage = errMsg.String
		if compiledAt.Valid {
			s.CompiledAt = &compiledAt.Time
		}
		result[s.ArticleID] = &s
	}
	return result, rows.Err()
}

// StartWorker starts the background compilation worker pool. It is safe to
// call multiple times (sync.Once-guarded). The worker picks up pending jobs,
// runs compileBoth, and updates the queue row with success/failure. Call
// StopWorker during graceful shutdown.
// numWorkers controls how many compilations run in parallel (bounded further
// by compileSem in compileBoth to avoid fork bombs).
func StartWorker(dbx *sql.DB, numWorkers int) {
	workerOnce.Do(func() {
		if numWorkers < 1 {
			numWorkers = 2
		}
		workerCtx, workerCancel = context.WithCancel(context.Background())

		// Recover stale 'compiling' jobs from a previous crash — reset them
		// to 'pending' so they get picked up immediately.
		go recoverStaleJobs(dbx)

		for i := 0; i < numWorkers; i++ {
			workerWg.Add(1)
			go runWorker(dbx, i)
		}
		slog.Info("typst: async compile worker started", "workers", numWorkers)
	})
}

// StopWorker signals all workers to exit and waits for them to finish.
// Called during graceful shutdown (Server.Close()).
func StopWorker() {
	if workerCancel != nil {
		workerCancel()
	}
	workerWg.Wait()
	slog.Info("typst: async compile worker stopped")
}

// recoverStaleJobs resets any jobs stuck in 'compiling' state (from a
// previous crash) back to 'pending' so they are re-processed.
func recoverStaleJobs(dbx *sql.DB) {
	if dbx == nil {
		return
	}
	cutoff := time.Now().Add(-staleCompileTimeout)
	res, err := dbx.Exec(
		`UPDATE typst_compile_queue SET status = 'pending', error_message = 'recovered from previous crash'
		 WHERE status = 'compiling' AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		slog.Warn("typst: recover stale jobs failed", "err", err)
		return
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		slog.Info("typst: recovered stale compiling jobs", "count", n)
	}
}

// runWorker is the main worker loop: poll for pending jobs, claim one, compile, update status.
func runWorker(dbx *sql.DB, id int) {
	defer workerWg.Done()
	slog.Debug("typst: worker started", "worker_id", id)
	for {
		select {
		case <-workerCtx.Done():
			slog.Debug("typst: worker exiting", "worker_id", id)
			return
		default:
		}

		job := claimNextJob(dbx)
		if job == nil {
			select {
			case <-workerCtx.Done():
				return
			case <-time.After(pollInterval):
				continue
			}
		}

		slog.Info("typst: worker compiling", "worker_id", id, "article_id", job.articleID, "attempt", job.attempts+1)
		compileErr := compileAndStore(dbx, job.articleID)

		if compileErr != nil {
			newAttempts := job.attempts + 1
			status := "failed"
			if newAttempts < maxQueueAttempts {
				status = "pending"
			}
			errMsg := compileErr.Error()
			if len(errMsg) > 2000 {
				errMsg = errMsg[:2000]
			}
			var updateErr error
			if newAttempts < maxQueueAttempts {
				_, updateErr = dbx.Exec(
					`UPDATE typst_compile_queue SET status = ?, error_message = ?, attempts = ?
					 WHERE article_id = ?`,
					status, errMsg, newAttempts, job.articleID,
				)
			} else {
				_, updateErr = dbx.Exec(
					`UPDATE typst_compile_queue SET status = 'failed', error_message = ?, attempts = ?
					 WHERE article_id = ?`,
					errMsg, newAttempts, job.articleID,
				)
			}
			if updateErr != nil {
				slog.Error("typst: failed to update queue status", "article_id", job.articleID, "err", updateErr)
			}
			slog.Warn("typst: compile failed", "article_id", job.articleID, "attempt", newAttempts, "err", compileErr)
		} else {
			_, err := dbx.Exec(
				`UPDATE typst_compile_queue SET status = 'success', error_message = NULL,
				   compiled_at = CURRENT_TIMESTAMP
				 WHERE article_id = ?`,
				job.articleID,
			)
			if err != nil {
				slog.Error("typst: failed to mark success", "article_id", job.articleID, "err", err)
			}
			slog.Info("typst: compile succeeded", "article_id", job.articleID)
		}
	}
}

type queuedJob struct {
	articleID int
	attempts  int
}

// claimNextJob atomically claims the next pending job (oldest first) using
// MySQL's SELECT ... FOR UPDATE SKIP LOCKED. If two workers race, one will
// get the row and mark it 'compiling'; the other will see no pending rows
// and back off.
func claimNextJob(dbx *sql.DB) *queuedJob {
	if dbx == nil {
		return nil
	}

	tx, err := dbx.Begin()
	if err != nil {
		slog.Error("typst: begin tx for job claim", "err", err)
		return nil
	}
	defer tx.Rollback()

	var id, attempts int
	err = tx.QueryRow(
		`SELECT article_id, attempts FROM typst_compile_queue
		 WHERE status = 'pending'
		 ORDER BY created_at ASC LIMIT 1
		 FOR UPDATE SKIP LOCKED`,
	).Scan(&id, &attempts)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("typst: claim job query", "err", err)
		}
		return nil
	}

	_, err = tx.Exec(
		`UPDATE typst_compile_queue SET status = 'compiling'
		 WHERE article_id = ? AND status = 'pending'`,
		id,
	)
	if err != nil {
		slog.Error("typst: mark job as compiling", "article_id", id, "err", err)
		return nil
	}

	if err := tx.Commit(); err != nil {
		slog.Error("typst: commit job claim", "err", err)
		return nil
	}

	return &queuedJob{articleID: id, attempts: attempts}
}

// compileAndStore fetches the article source, compiles both HTML and PDF,
// and writes the result to typst_cache. Returns the compile error (nil = success).
func compileAndStore(dbx *sql.DB, articleID int) error {
	var source string
	err := dbx.QueryRow(`SELECT content FROM articles WHERE id = ? AND type = 'typst'`, articleID).Scan(&source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = dbx.Exec(`DELETE FROM typst_compile_queue WHERE article_id = ?`, articleID)
			return nil
		}
		return fmt.Errorf("fetch article source: %w", err)
	}

	// SetDB is called once at startup before StartWorker, so the package-level
	// db is already configured and safe for concurrent use.
	pdf, html, depIDs, err := compileBoth(articleID, source)
	if err != nil {
		return err
	}
	if html == "" {
		return errors.New("HTML compile produced empty output")
	}
	if len(pdf) == 0 {
		return errors.New("PDF compile produced empty output (typst CLI failed or syntax error)")
	}

	depStr := formatDepList(depIDs)
	if _, err := dbx.Exec(
		`INSERT INTO typst_cache (article_id, html_content, pdf_content, dependencies)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   html_content = VALUES(html_content),
		   pdf_content  = VALUES(pdf_content),
		   dependencies = VALUES(dependencies),
		   compiled_at  = CURRENT_TIMESTAMP`,
		articleID, html, pdf, depStr,
	); err != nil {
		return fmt.Errorf("cache write failed: %w", err)
	}

	// Re-compile any articles that depend on this one (cascade).
	if err := EnqueueDependents(dbx, articleID); err != nil {
		slog.Warn("typst: failed to enqueue dependents after compile", "article_id", articleID, "err", err)
	}

	return nil
}
