package typst

import (
	"context"
	"database/sql"
	"sync"
)

// AfterCompileFunc is the post-compile hook signature. The callback receives
// a context, the article ID, the raw HTML body from typst, and the resolved
// dependency IDs. It is invoked synchronously inside CompileAndCache /
// compileAndStore after the cache row is written, so callers can sync derived
// state (e.g. articles.rendered_html) without creating a circular dependency
// between the typst and rendercache packages.
type AfterCompileFunc func(ctx context.Context, articleID int, htmlBody string, depIDs []int)

// Worker is the stateful typst service: it owns the DB connection used for
// cache reads/writes, the post-compile hook, the compile-concurrency semaphore,
// and the async compile worker pool. Construct one with NewWorker at startup
// and inject it into the API Server and content layer; do NOT share package-
// level state across tests.
//
// All public methods are safe for concurrent use.
type Worker struct {
	db *sql.DB

	// afterCompile is fired after a successful cache write. May be nil.
	afterCompile AfterCompileFunc

	// compileSem bounds the number of concurrent typst CLI subprocesses to
	// avoid fork-bomb / disk exhaustion under load.
	compileSem chan struct{}

	// workspaceOnce / workspaceDir pin the typst project root. Lazy so tests
	// that never compile don't pollute the source tree.
	workspaceOnce sync.Once
	workspaceDir  string

	// worker pool control. workerCtx/workerCancel are set by StartWorker and
	// read by runWorker loops; guarded by workerMu for StopWorker's "stop
	// only if started" check.
	workerMu     sync.Mutex
	workerOnce   sync.Once
	workerWg     sync.WaitGroup
	workerCtx    context.Context
	workerCancel context.CancelFunc
}

// NewWorker constructs a Worker bound to the given DB. The compile semaphore
// is sized by maxConcurrent (package constant). Call SetWorkspaceDir before
// the first compile if you want a non-default workspace, and SetAfterCompile
// to wire the post-compile hook.
func NewWorker(db *sql.DB) *Worker {
	return &Worker{
		db:         db,
		compileSem: make(chan struct{}, maxConcurrent),
	}
}

// SetAfterCompile installs (or replaces) the post-compile hook. Safe to call
// before StartWorker; not safe to call concurrently with an in-flight compile
// (the hook is read once per compile under no lock — set it once at startup,
// like the old package-level var).
func (w *Worker) SetAfterCompile(fn AfterCompileFunc) { w.afterCompile = fn }

// DB returns the bound *sql.DB (used by callers that still want to issue
// their own queries against typst_* tables through the same pool).
func (w *Worker) DB() *sql.DB { return w.db }
