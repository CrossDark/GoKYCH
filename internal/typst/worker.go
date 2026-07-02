package typst

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	coredb "gokych/internal/core/db"
)

const (
	maxQueueAttempts    = 3
	pollInterval        = 2 * time.Second
	staleCompileTimeout = 5 * time.Minute
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
//
// Deprecated: Use EnqueueCompileCtx instead.
func (w *Worker) EnqueueCompile(articleID int) error {
	return w.EnqueueCompileCtx(context.TODO(), articleID)
}

// EnqueueCompileCtx is the context-aware version of EnqueueCompile.
func (w *Worker) EnqueueCompileCtx(ctx context.Context, articleID int) error {
	if w == nil || w.db == nil {
		return errors.New("typst: db not configured")
	}
	if articleID <= 0 {
		return errors.New("typst: invalid article id")
	}
	dbx := w.db
	if !Available() {
		_, err := dbx.ExecContext(ctx,
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
	_, err := dbx.ExecContext(ctx,
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
// for re-compilation. Used after UpdateArticle/DeleteArticle to keep caches
// fresh. Shares buildReverseDepMap + transitiveDependents with
// InvalidateDependents so the two BFS walks stay identical.
//
// Deprecated: Use EnqueueDependentsCtx instead.
func (w *Worker) EnqueueDependents(changedID int) error {
	return w.EnqueueDependentsCtx(context.TODO(), changedID)
}

// EnqueueDependentsCtx is the context-aware version of EnqueueDependents.
func (w *Worker) EnqueueDependentsCtx(ctx context.Context, changedID int) error {
	if w == nil || w.db == nil || changedID <= 0 {
		return nil
	}
	reverseDep, err := buildReverseDepMapCtx(ctx, w.db)
	if err != nil {
		return err
	}
	toEnqueue := transitiveDependents(reverseDep, changedID)

	for _, aid := range toEnqueue {
		if err := w.EnqueueCompileCtx(ctx, aid); err != nil {
			slog.Warn("typst: failed to enqueue dependent", "article_id", aid, "err", err)
		}
	}

	// Also invalidate old caches for dependents so readers see the placeholder
	// instead of stale HTML.
	if len(toEnqueue) > 0 {
		if err := w.InvalidateDependentsCtx(ctx, changedID); err != nil {
			slog.Warn("typst: failed to invalidate dependent caches", "err", err)
		}
	}

	return nil
}

// GetCompileStatus returns the current compilation status for an article.
// Returns nil if no queue entry exists (meaning it was either never queued
// or compiled successfully before the queue was introduced — treat as "ready"
// if typst_cache has the content).
//
// Deprecated: Use GetCompileStatusCtx instead.
func (w *Worker) GetCompileStatus(articleID int) (*CompileStatus, error) {
	return w.GetCompileStatusCtx(context.TODO(), articleID)
}

// GetCompileStatusCtx is the context-aware version of GetCompileStatus.
func (w *Worker) GetCompileStatusCtx(ctx context.Context, articleID int) (*CompileStatus, error) {
	if w == nil || w.db == nil || articleID <= 0 {
		return nil, nil
	}
	var s CompileStatus
	var errMsg sql.NullString
	var compiledAt sql.NullTime
	err := w.db.QueryRowContext(ctx,
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
//
// Deprecated: Use GetPendingCompileStatusesCtx instead.
func (w *Worker) GetPendingCompileStatuses(articleIDs []int) (map[int]*CompileStatus, error) {
	return w.GetPendingCompileStatusesCtx(context.TODO(), articleIDs)
}

// GetPendingCompileStatusesCtx is the context-aware version of GetPendingCompileStatuses.
func (w *Worker) GetPendingCompileStatusesCtx(ctx context.Context, articleIDs []int) (map[int]*CompileStatus, error) {
	if w == nil || w.db == nil || len(articleIDs) == 0 {
		return nil, nil
	}
	result := make(map[int]*CompileStatus)
	args := make([]any, len(articleIDs))
	for i, id := range articleIDs {
		args[i] = id
	}
	q := `SELECT article_id, status, error_message, attempts, created_at, updated_at, compiled_at
	      FROM typst_compile_queue WHERE article_id IN (` + coredb.Placeholders(len(articleIDs)) + `)
	      AND status IN ('pending','compiling','failed')`
	rows, err := w.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
// call multiple times (sync.Once-guarded on the Worker). The worker picks up
// pending jobs, runs compileBoth, and updates the queue row with
// success/failure. Call StopWorker during graceful shutdown.
// numWorkers controls how many compilations run in parallel (bounded further
// by compileSem in compileBoth to avoid fork bombs).
func (w *Worker) StartWorker(numWorkers int) {
	w.workerOnce.Do(func() {
		if numWorkers < 1 {
			numWorkers = 2
		}
		w.workerCtx, w.workerCancel = context.WithCancel(context.Background())

		// Recover stale 'compiling' jobs from a previous crash — reset them
		// to 'pending' so they get picked up immediately.
		go w.recoverStaleJobs(w.workerCtx)

		for i := 0; i < numWorkers; i++ {
			w.workerWg.Add(1)
			go w.runWorker(i)
		}
		slog.Info("typst: async compile worker started", "workers", numWorkers)
	})
}

// StopWorker signals all workers to exit and waits for them to finish.
// Called during graceful shutdown. No-op if StartWorker was never called.
func (w *Worker) StopWorker() {
	w.workerMu.Lock()
	cancel := w.workerCancel
	w.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.workerWg.Wait()
	slog.Info("typst: async compile worker stopped")
}

// recoverStaleJobs resets any jobs stuck in 'compiling' state (from a
// previous crash) back to 'pending' so they are re-processed.
func (w *Worker) recoverStaleJobs(ctx context.Context) {
	if w.db == nil {
		return
	}
	cutoff := time.Now().Add(-staleCompileTimeout)
	res, err := w.db.ExecContext(ctx,
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
func (w *Worker) runWorker(id int) {
	defer w.workerWg.Done()
	slog.Debug("typst: worker started", "worker_id", id)
	ctx := w.workerCtx
	for {
		select {
		case <-ctx.Done():
			slog.Debug("typst: worker exiting", "worker_id", id)
			return
		default:
		}

		job := w.claimNextJobCtx(ctx)
		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
				continue
			}
		}

		slog.Info("typst: worker compiling", "worker_id", id, "article_id", job.articleID, "attempt", job.attempts+1)
		compileErr := w.compileAndStoreCtx(ctx, job.articleID)

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
				_, updateErr = w.db.ExecContext(ctx,
					`UPDATE typst_compile_queue SET status = ?, error_message = ?, attempts = ?
					 WHERE article_id = ?`,
					status, errMsg, newAttempts, job.articleID,
				)
			} else {
				_, updateErr = w.db.ExecContext(ctx,
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
			_, err := w.db.ExecContext(ctx,
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

// claimNextJobCtx atomically claims the next pending job (oldest first) using
// MySQL's SELECT ... FOR UPDATE SKIP LOCKED. If two workers race, one will
// get the row and mark it 'compiling'; the other will see no pending rows
// and back off.
func (w *Worker) claimNextJobCtx(ctx context.Context) *queuedJob {
	if w.db == nil {
		return nil
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("typst: begin tx for job claim", "err", err)
		return nil
	}
	defer tx.Rollback()

	var id, attempts int
	err = tx.QueryRowContext(ctx,
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

	_, err = tx.ExecContext(ctx,
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

// Deprecated: Use claimNextJobCtx instead.
func (w *Worker) claimNextJob() *queuedJob {
	return w.claimNextJobCtx(context.TODO())
}

// compileAndStoreCtx fetches the article source, compiles both HTML and PDF,
// and writes the result to typst_cache. Returns the compile error (nil = success).
// Shares storeCompileResultCtx with CompileAndCacheCtx so the cache-write +
// dep-sync + afterCompile hook logic isn't duplicated.
func (w *Worker) compileAndStoreCtx(ctx context.Context, articleID int) error {
	var source string
	err := w.db.QueryRowContext(ctx, `SELECT content FROM articles WHERE id = ? AND type = 'typst'`, articleID).Scan(&source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = w.db.ExecContext(ctx, `DELETE FROM typst_compile_queue WHERE article_id = ?`, articleID)
			return nil
		}
		return fmt.Errorf("fetch article source: %w", err)
	}

	pdf, html, depIDs, err := compileBothCtx(ctx, w.db, articleID, source)
	if err != nil {
		return err
	}
	if html == "" {
		return errors.New("HTML compile produced empty output")
	}
	if len(pdf) == 0 {
		return errors.New("PDF compile produced empty output (typst CLI failed or syntax error)")
	}

	if err := w.storeCompileResultCtx(ctx, articleID, html, pdf, depIDs); err != nil {
		return err
	}

	// Re-compile any articles that depend on this one (cascade).
	if err := w.EnqueueDependentsCtx(ctx, articleID); err != nil {
		slog.Warn("typst: failed to enqueue dependents after compile", "article_id", articleID, "err", err)
	}

	return nil
}

// Deprecated: Use compileAndStoreCtx instead.
func (w *Worker) compileAndStore(articleID int) error {
	return w.compileAndStoreCtx(context.TODO(), articleID)
}
