package typst

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	coredb "gokych/internal/core/db"
)

//go:embed assets/*.typ
var embeddedTypstFS embed.FS

const (
	compileTimeout = 30 * time.Second
	maxConcurrent  = 4
)

// compileSem bounds the number of concurrent typst CLI invocations to avoid
// fork-bomb / disk exhaustion under load. HTML and PDF compilations each
// acquire one slot (so a parallel compileBoth call uses 2 of the 4 slots).
// Kept at package scope because the typst CLI is a system-wide resource —
// even a test calling CompileHTML directly (no Worker) should participate
// in the same cap.
var compileSem = make(chan struct{}, maxConcurrent)

// workspaceDir is the directory typst compiles in. Relative imports
// (e.g. `#import "template.typ"`) resolve from here, and any image / asset
// references the user puts in the article can sit alongside the .typ
// source. The path is resolved lazily on first use (not at package init),
// so `go test` (which changes cwd to the package dir) doesn't accidentally
// write to a polluted `./data/typst/` next to the test source.
//
// Set explicitly with SetWorkspaceDir at startup (production), or rely on
// the env-var fallback (GOKYCH_TYPST_DIR) / relative default
// ("data/typst", interpreted relative to the binary's cwd at first
// compile). The lazy resolution means tests that don't call CompileHTML
// (e.g. the materialize / cleanup unit tests) never trigger it.
//
// NOTE: db / AfterCompileFunc used to live here as package-level mutable
// state; they've moved to the Worker struct (see worker_state.go) so
// concurrent startup can't race on SetDB and tests can run with isolated
// DBs. The package-level compileSem and workspaceDir remain because they
// represent process-wide physical resources (the CLI subprocess pool and
// the on-disk workspace directory), not per-Worker state.
var (
	workspaceDirOnce sync.Once
	workspaceDir     string
	uploadsDir       string
	avatarsDir       string
)

// SetAssetsDirs configures the on-disk paths for user-uploaded files and
// avatars. Called once at startup after config load, alongside SetWorkspaceDir.
// Symlinks are created *inside* the workspace dir so that typst source can
// reference uploaded images via relative paths:
//
//	#image("uploads/photo.jpg")
//	#image("avatars/avatar_xxx.jpg")
//
// A source-rewriting pass in compileBothCtx also translates the web-style
// absolute paths "/uploads/..." and "/avatars/..." to their workspace-relative
// equivalents, so authors can copy-paste the URL they see in the upload
// picker without manual editing.
func SetAssetsDirs(uploads, avatars string) {
	uploadsDir = uploads
	avatarsDir = avatars
}

// SetWorkspaceDir sets the absolute (or process-relative) path to the
// typst workspace. Production binaries should call this once at startup
// after config is loaded, passing an absolute path like
// cfg.DataRoot() + "/typst". Calling it more than once is a no-op.
func SetWorkspaceDir(path string) {
	workspaceDirOnce.Do(func() {
		workspaceDir = path
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			slog.Error("typst: create workspace dir", "dir", workspaceDir, "err", err)
			return
		}
		cleanupLeakedInputs(workspaceDir)
		materializeAssets(workspaceDir)
	})
}

// resolveWorkspaceDir picks the workspace dir based on env var or default.
// Only called from ensureWorkspace if SetWorkspaceDir wasn't called first.
func resolveWorkspaceDir() string {
	if d := os.Getenv("GOKYCH_TYPST_DIR"); d != "" {
		return d
	}
	return "data/typst"
}

// ensureWorkspace materializes the workspace once per process. Tests that
// use the helpers directly (materializeAssets / cleanupLeakedInputs) never
// hit this path; production binaries should call SetWorkspaceDir from
// main, which also bypasses this fallback.
func ensureWorkspace() {
	workspaceDirOnce.Do(func() {
		workspaceDir = resolveWorkspaceDir()
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			slog.Error("typst: create workspace dir", "dir", workspaceDir, "err", err)
			return
		}
		cleanupLeakedInputs(workspaceDir)
		materializeAssets(workspaceDir)
	})
}

// WorkspaceDir returns the path to the typst workspace dir. Triggers lazy
// initialization on first call. Used by callers that need to surface the
// path (admin UI, docs, error messages) without reimplementing the
// env-var fallback.
func WorkspaceDir() string {
	ensureWorkspace()
	return workspaceDir
}

// Path returns the full path to the typst CLI binary, or empty string.
// Searches: TYPST_PATH env, then $PATH, then common locations.
func Path() string {
	if p := os.Getenv("TYPST_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("typst"); err == nil {
		return p
	}
	common := []string{
		"/opt/homebrew/bin/typst",
		"/usr/local/bin/typst",
		"/usr/bin/typst",
	}
	for _, p := range common {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Available reports whether the typst CLI is installed.
func Available() bool { return Path() != "" }

// envWhitelist returns a minimal environment for the typst subprocess, avoiding
// leakage of parent-process secrets (SESSION_SECRET, DB_PASSWORD, ...) via env.
func envWhitelist() []string {
	full := os.Environ()
	keep := make([]string, 0, 8)
	for _, kv := range full {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch key {
		case "PATH", "HOME", "USER", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TYPST_PATH":
			keep = append(keep, kv)
		}
	}
	return keep
}

// CompileHTML compiles a typst source string to HTML body content.
// Uses a temporary file for input and output. The subprocess is bounded by a
// 30s timeout and a concurrency semaphore (maxConcurrent) to prevent a
// pathological document from pinning goroutines or exhausting resources.
// Note: cross-article @imports are NOT resolved here (no DB), so this path
// is only suitable for single-line / standalone snippets. Use Worker.CompileAndCache
// for full articles that may reference other typst articles.
func CompileHTML(source string) (string, error) {
	return CompileHTMLCtx(context.Background(), source)
}

// CompileHTMLCtx is the context-aware version of CompileHTML.
func CompileHTMLCtx(ctx context.Context, source string) (string, error) {
	_, html, _, err := compileBothCtx(ctx, nil, 0, source)
	return html, err
}

// CompilePDF compiles a typst source string to PDF bytes. Same timeout +
// concurrency guards as CompileHTML. Note: this calls the typst CLI a second
// time on the same source — typst doesn't support emitting both formats in
// one invocation, so PDF users pay for one extra compile (the compileHTML
// fast-path doesn't share work). The result is cached separately via
// Worker.CompilePDFCached so subsequent PDF requests are free.
func CompilePDF(source string) ([]byte, error) {
	return CompilePDFCtx(context.Background(), source)
}

// CompilePDFCtx is the context-aware version of CompilePDF.
func CompilePDFCtx(ctx context.Context, source string) ([]byte, error) {
	pdf, _, _, err := compileBothCtx(ctx, nil, 0, source)
	return pdf, err
}

// compileBothCtx runs the typst CLI twice on the same source — once for HTML
// and once for PDF — and returns both. Both invocations run with
// `cmd.Dir = workspaceDir` so relative imports in the source (e.g.
// `#import "template.typ"`) resolve from there. The input file and the two
// output files use a per-invocation unique prefix (UnixNano + PID) so the
// semaphore's maxConcurrent goroutines can run without trampling each
// other.
//
// currentArticleID > 0 enables cross-article @import resolution: before
// writing the input file, resolveDependencies walks @slug references,
// writes dep files to workspaceDir, rewrites the source, and returns the
// list of resolved dependency IDs. Pass 0 to skip resolution (used for
// single-line / standalone snippets). dbx is the DB used for dependency
// resolution; pass nil when currentArticleID == 0 (the package-level
// CompileHTML / CompilePDF helpers do exactly that).
func compileBothCtx(ctx context.Context, dbx *sql.DB, currentArticleID int, source string) (pdf []byte, html string, depIDs []int, err error) {
	bin := Path()
	if bin == "" {
		return nil, "", nil, fmt.Errorf("typst: CLI not found")
	}
	// Trigger lazy workspace setup (mkdir + materialize + leak cleanup) on
	// first compile. Tests that don't call CompileHTML never hit this path.
	ensureWorkspace()

	// Ensure uploads/ and avatars/ symlinks exist inside the workspace.
	// Called per-compile (not just at startup) so that the symlinks are
	// created correctly regardless of whether SetAssetsDirs was called
	// before or after SetWorkspaceDir. linkAssetDirs is idempotent — a
	// correct existing symlink is left untouched.
	linkAssetDirs(workspaceDir)

	// Rewrite web-style absolute asset paths ("/uploads/...", "/avatars/...")
	// to workspace-relative paths BEFORE dependency resolution, so that
	// dependency articles also benefit from the rewrite.
	source = rewriteAssetPaths(source)

	// Resolve cross-article @imports (writes .dep_N.typ files into workspace).
	var depFiles []string
	if currentArticleID > 0 && dbx != nil {
		res, rerr := resolveDependenciesCtx(ctx, dbx, workspaceDir, currentArticleID, source)
		if rerr != nil {
			return nil, "", nil, rerr
		}
		source = res.source
		depFiles = res.depFiles
		depIDs = res.depIDs
	}

	// Ensure dep files are cleaned up after compilation (success or failure).
	defer func() {
		for _, f := range depFiles {
			_ = os.Remove(f)
		}
	}()

	// Bound the subprocess lifetime so a hang can't pin a goroutine forever,
	// while respecting the caller's context.
	compileCtx, cancel := context.WithTimeout(ctx, compileTimeout)
	defer cancel()

	// Per-invocation unique filename. PID disambiguates two compiles that
	// happen in the same nanosecond across forked workers (not currently
	// possible but cheap insurance).
	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), os.Getpid())
	inputName := ".input_" + suffix + ".typ"
	htmlName := ".output_" + suffix + ".html"
	pdfName := ".output_" + suffix + ".pdf"
	inputPath := filepath.Join(workspaceDir, inputName)
	htmlPath := filepath.Join(workspaceDir, htmlName)
	pdfPath := filepath.Join(workspaceDir, pdfName)
	defer os.Remove(inputPath)
	defer os.Remove(htmlPath)
	defer os.Remove(pdfPath)

	if err := os.WriteFile(inputPath, []byte(source), 0600); err != nil {
		return nil, "", nil, fmt.Errorf("typst: write temp input: %w", err)
	}

	// Compile HTML and PDF in parallel using errgroup. Each goroutine
	// acquires its own semaphore slot (maxConcurrent=4 caps total typst
	// CLI processes). Both share the same compileCtx (30s timeout) and
	// run concurrently to cut wall-clock time roughly in half.
	var (
		htmlBytes    []byte
		htmlErr      error
		pdfBytes     []byte
		pdfErr       error
		pdfTimedOut  bool
		pdfCtxCancel bool
	)

	g, _ := errgroup.WithContext(compileCtx)

	g.Go(func() error {
		compileSem <- struct{}{}
		defer func() { <-compileSem }()

		cmd := exec.CommandContext(compileCtx, bin, "compile",
			"--format", "html",
			"--features", "html",
			inputName, htmlName,
		)
		cmd.Env = envWhitelist()
		cmd.Dir = workspaceDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			if compileCtx.Err() == context.DeadlineExceeded {
				htmlErr = fmt.Errorf("typst: compile timed out after %s", compileTimeout)
				return htmlErr
			}
			if ctx.Err() != nil {
				htmlErr = ctx.Err()
				return htmlErr
			}
			htmlErr = fmt.Errorf("typst: html compile failed: %w\n%s", err, string(output))
			return htmlErr
		}
		htmlBytes, htmlErr = os.ReadFile(htmlPath)
		if htmlErr != nil {
			htmlErr = fmt.Errorf("typst: read html output: %w", htmlErr)
			return htmlErr
		}
		return nil
	})

	g.Go(func() error {
		compileSem <- struct{}{}
		defer func() { <-compileSem }()

		cmdPDF := exec.CommandContext(compileCtx, bin, "compile", inputName, pdfName)
		cmdPDF.Env = envWhitelist()
		cmdPDF.Dir = workspaceDir
		output, err := cmdPDF.CombinedOutput()
		if err != nil {
			if compileCtx.Err() == context.DeadlineExceeded {
				pdfTimedOut = true
				return nil
			}
			if ctx.Err() != nil {
				pdfCtxCancel = true
				return nil
			}
			pdfErr = fmt.Errorf("typst: pdf compile failed: %w\n%s", err, string(output))
			return nil
		}
		pdfBytes, pdfErr = os.ReadFile(pdfPath)
		if pdfErr != nil {
			pdfErr = fmt.Errorf("typst: read pdf output: %w", pdfErr)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, "", nil, err
	}

	if htmlErr != nil {
		return nil, "", nil, htmlErr
	}
	html = extractBody(string(htmlBytes))

	if pdfTimedOut {
		slog.Warn("typst pdf compile timed out", "timeout", compileTimeout)
		return nil, html, depIDs, nil
	}
	if pdfCtxCancel {
		slog.Warn("typst pdf compile cancelled by context")
		return nil, html, depIDs, nil
	}
	if pdfErr != nil {
		slog.Warn("typst pdf compile failed", "err", pdfErr)
		return nil, html, depIDs, nil
	}
	pdf = pdfBytes

	return pdf, html, depIDs, nil
}

// Deprecated: Use compileBothCtx instead.
func compileBoth(dbx *sql.DB, currentArticleID int, source string) (pdf []byte, html string, depIDs []int, err error) {
	return compileBothCtx(context.Background(), dbx, currentArticleID, source)
}

// storeCompileResultCtx writes the compiled HTML + PDF into typst_cache and
// syncs the dependency rows in article_deps, then fires the afterCompile
// hook. Shared between Worker.CompileAndCacheCtx (publish-time eager path)
// and Worker.compileAndStoreCtx (async worker path) so the two don't drift —
// the previous copy-pasted duplicates had subtly different error wording.
func (w *Worker) storeCompileResultCtx(ctx context.Context, articleID int, html string, pdf []byte, depIDs []int) error {
	if w.db == nil {
		return errors.New("typst: db not configured")
	}
	depStr := formatDepList(depIDs)
	if _, err := w.db.ExecContext(ctx,
		`INSERT INTO typst_cache (article_id, html_content, pdf_content, dependencies)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   html_content = VALUES(html_content),
		   pdf_content  = VALUES(pdf_content),
		   dependencies = VALUES(dependencies),
		   compiled_at  = CURRENT_TIMESTAMP`,
		articleID, html, pdf, depStr,
	); err != nil {
		return fmt.Errorf("typst: cache write failed: %w", err)
	}

	// Sync dependencies to article_deps for cascading invalidation. Errors
	// here break cascade invalidation, so log instead of swallowing silently
	// (the old `_, _ =` swallowed them and left caches inconsistent).
	if _, derr := w.db.ExecContext(ctx, `DELETE FROM article_deps WHERE article_id = ?`, articleID); derr != nil {
		slog.Warn("typst: clear article_deps failed", "article_id", articleID, "err", derr)
	} else {
		for _, did := range depIDs {
			if _, ierr := w.db.ExecContext(ctx,
				`INSERT IGNORE INTO article_deps (article_id, depends_on_id) VALUES (?, ?)`,
				articleID, did,
			); ierr != nil {
				slog.Warn("typst: insert article_deps failed", "article_id", articleID, "dep_id", did, "err", ierr)
			}
		}
	}

	// Fire post-compile hook.
	if w.afterCompile != nil {
		w.afterCompile(ctx, articleID, html, depIDs)
	}
	return nil
}

// Deprecated: Use storeCompileResultCtx instead.
func (w *Worker) storeCompileResult(articleID int, html string, pdf []byte, depIDs []int) error {
	return w.storeCompileResultCtx(context.TODO(), articleID, html, pdf, depIDs)
}

// CompileAndCache is the eager-precompile path used at article publish time:
// it runs the typst CLI once to produce BOTH HTML and PDF, then persists
// both into typst_cache. After publish, readers hit CompileHTMLCached /
// CompilePDFCached (read-only SELECT) and never pay the compile cost.
//
// Fail-fast: a partial result (e.g. HTML ok but PDF empty) is treated as a
// hard error — we never write a half-populated row, because a follow-up
// read would see "HTML present, PDF missing" and the PDF endpoint would 404
// for an article that exists. Returning an error lets the caller reject
// the publish instead of leaking inconsistent state.
//
// Requires the Worker to have been constructed with a non-nil DB. If typst
// isn't installed the error surfaces as a clear "CLI not found" message,
// which the admin handler translates into a 400 with a hint.
//
// Deprecated: Use CompileAndCacheCtx instead.
func (w *Worker) CompileAndCache(articleID int, source string) error {
	return w.CompileAndCacheCtx(context.TODO(), articleID, source)
}

// CompileAndCacheCtx is the context-aware version of CompileAndCache.
func (w *Worker) CompileAndCacheCtx(ctx context.Context, articleID int, source string) error {
	if w == nil || w.db == nil {
		return errors.New("typst: db not configured (construct Worker with NewWorker)")
	}
	if articleID <= 0 {
		return errors.New("typst: invalid article id")
	}
	pdf, html, depIDs, err := compileBothCtx(ctx, w.db, articleID, source)
	if err != nil {
		return err
	}
	if html == "" {
		return errors.New("typst: HTML compile produced empty output")
	}
	if len(pdf) == 0 {
		return errors.New("typst: PDF compile produced empty output (typst CLI failed or syntax error)")
	}
	if err := w.storeCompileResultCtx(ctx, articleID, html, pdf, depIDs); err != nil {
		return err
	}
	slog.Info("typst: compiled and cached", "article_id", articleID, "deps", len(depIDs))
	return nil
}

// CompileHTMLCached is the READ-ONLY cache lookup for articleID. It does
// NOT fall back to CompileHTML — a missing row is a hard miss that the
// renderer must surface as a "pending compile" placeholder, because the
// compile happens at publish time (see CompileAndCache). If you need
// unconditional compilation, call CompileHTML(source) directly.
//
// A nil Worker (or one with no DB) returns a clear "no cache available"
// error rather than silently recompiling (the previous behaviour masked
// the bug where a fresh DB would re-fork typst on every request).
//
// Deprecated: Use CompileHTMLCachedCtx instead.
func (w *Worker) CompileHTMLCached(articleID int, source string) (string, error) {
	return w.CompileHTMLCachedCtx(context.TODO(), articleID, source)
}

// CompileHTMLCachedCtx is the context-aware version of CompileHTMLCached.
func (w *Worker) CompileHTMLCachedCtx(ctx context.Context, articleID int, source string) (string, error) {
	if w == nil || w.db == nil || articleID <= 0 {
		return "", fmt.Errorf("typst: cache unavailable (db configured=%t, articleID=%d)", w != nil && w.db != nil, articleID)
	}
	var html string
	err := w.db.QueryRowContext(ctx,
		`SELECT html_content FROM typst_cache WHERE article_id = ?`, articleID,
	).Scan(&html)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("typst: no cached HTML for article %d (publish-time compile pending?)", articleID)
		}
		slog.Error("typst cache lookup", "article_id", articleID, "err", err)
		return "", err
	}
	if html == "" {
		return "", fmt.Errorf("typst: empty HTML cache for article %d", articleID)
	}
	return html, nil
}

// CompilePDFCached is the READ-ONLY cache lookup for articleID. Same
// fail-fast contract as CompileHTMLCached: a miss is an error, not a
// trigger for fresh compilation. The PDF endpoint translates this into a
// 503/404 with a "PDF not yet generated" message.
//
// Deprecated: Use CompilePDFCachedCtx instead.
func (w *Worker) CompilePDFCached(articleID int, source string) ([]byte, error) {
	return w.CompilePDFCachedCtx(context.TODO(), articleID, source)
}

// CompilePDFCachedCtx is the context-aware version of CompilePDFCached.
func (w *Worker) CompilePDFCachedCtx(ctx context.Context, articleID int, source string) ([]byte, error) {
	if w == nil || w.db == nil || articleID <= 0 {
		return nil, fmt.Errorf("typst: cache unavailable (db configured=%t, articleID=%d)", w != nil && w.db != nil, articleID)
	}
	var pdf []byte
	err := w.db.QueryRowContext(ctx,
		`SELECT pdf_content FROM typst_cache WHERE article_id = ?`, articleID,
	).Scan(&pdf)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("typst: no cached PDF for article %d (publish-time compile pending?)", articleID)
		}
		slog.Error("typst pdf cache lookup", "article_id", articleID, "err", err)
		return nil, err
	}
	if len(pdf) == 0 {
		return nil, fmt.Errorf("typst: empty PDF cache for article %d", articleID)
	}
	return pdf, nil
}

// extractBody pulls the inner content from a full HTML document's <body> tag.
// The typst CLI outputs a complete HTML document with <!DOCTYPE>, <html>, <head>, <body>.
// We only need the body contents for injection into the site template.
var bodyRegex = regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)

func extractBody(html string) string {
	m := bodyRegex.FindStringSubmatch(html)
	if len(m) < 2 {
		// Fallback: return the full HTML (shouldn't happen in normal use).
		return html
	}
	return strings.TrimSpace(m[1])
}

// cleanupLeakedInputs removes any `.input_*.typ` / `.output_*.html` /
// `.output_*.pdf` / `.dep_*.typ` files left over from a previous crash
// (process kill, OOM, etc.). Logs the outcome so stale files don't go
// unnoticed.
func cleanupLeakedInputs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var cleaned, failed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".input_") ||
			strings.HasPrefix(name, ".output_") ||
			strings.HasPrefix(name, ".dep_") {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				failed++
			} else {
				cleaned++
			}
		}
	}
	if cleaned > 0 || failed > 0 {
		slog.Info("typst: cleaned leaked temp files", "cleaned", cleaned, "failed", failed)
	}
}

// materializeAssets writes the embedded `assets/*.typ` files into the
// workspace dir. Files that already exist are NOT overwritten (lets users
// customize template.typ and keep their edits across restarts). Best-effort:
// any error is logged and the workspace is still used as-is.
func materializeAssets(dir string) {
	entries, err := embeddedTypstFS.ReadDir("assets")
	if err != nil {
		// No assets embedded — fine, just log and continue.
		slog.Info("typst: no embedded assets to materialize", "err", err)
		return
	}
	for _, e := range entries {
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // user has a local copy; respect it
		}
		data, err := embeddedTypstFS.ReadFile("assets/" + e.Name())
		if err != nil {
			slog.Error("typst: read embedded asset", "file", e.Name(), "err", err)
			continue
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			slog.Error("typst: materialize asset", "file", e.Name(), "err", err)
			continue
		}
		slog.Info("typst: materialized asset", "file", dst)
	}
}

// linkAssetDirs creates (or refreshes) symlinks inside the workspace dir so
// that typst source can resolve relative paths like "uploads/foo.png" and
// "avatars/bar.jpg" to the real upload/avatar directories on disk.
//
// Why symlinks instead of copying:
//   - Uploads can be large (images, PDFs); copying on every startup wastes
//     disk and time.
//   - New uploads appear instantly without restarting the server.
//   - Symlinks are removed and re-created each call, so pointing at a
//     different directory (e.g. after a config reload) works cleanly.
//
// If the target directory doesn't exist (uploadsDir/avatarsDir not yet
// configured, as in tests), the symlink is skipped — typst will surface a
// clear "file not found" error for the referenced image, which is the
// correct behaviour.
func linkAssetDirs(workspace string) {
	links := []struct {
		linkName string
		target   string
	}{
		{"uploads", uploadsDir},
		{"avatars", avatarsDir},
	}
	for _, l := range links {
		if l.target == "" {
			continue
		}
		// Resolve to absolute so the symlink works regardless of cwd.
		absTarget, err := filepath.Abs(l.target)
		if err != nil {
			slog.Warn("typst: resolve asset dir", "name", l.linkName, "err", err)
			continue
		}
		linkPath := filepath.Join(workspace, l.linkName)
		// Remove any existing symlink / stale file at that path.
		if existing, err := os.Readlink(linkPath); err == nil {
			if existing == absTarget {
				continue // already correct
			}
			_ = os.Remove(linkPath)
		} else {
			// Not a symlink — if a regular file/dir exists here, remove
			// it (it would shadow the symlink we want to create).
			if _, serr := os.Stat(linkPath); serr == nil {
				_ = os.RemoveAll(linkPath)
			}
		}
		if err := os.Symlink(absTarget, linkPath); err != nil {
			slog.Warn("typst: create asset symlink", "link", linkPath, "target", absTarget, "err", err)
		} else {
			slog.Debug("typst: linked asset dir", "link", linkPath, "target", absTarget)
		}
	}
}

// assetPathRe matches web-style absolute paths in typst string literals and
// rewrites them to workspace-relative paths by stripping the leading slash:
//
//	#image("/uploads/foo.png")        →  #image("uploads/foo.png")
//	#image('/avatars/bar.jpg')        →  #image('avatars/bar.jpg')
//	#import "/uploads/lib.typ"        →  #import "uploads/lib.typ"
//	#include "/uploads/header.typ"    →  #include "uploads/header.typ"
//	#read("/uploads/data.csv")        →  #read("uploads/data.csv")
//	#bibliography("/uploads/refs.bib") →  #bibliography("uploads/refs.bib")
//
// The leading character class (^|[\s(,:=]) ensures we only match paths in
// positions where typst expects a filesystem path — function arguments,
// import/include statements, named parameters (with or without space after
// the colon), and variable assignments. Plain prose strings like
// "see /uploads/help.pdf" are NOT rewritten because the opening quote is
// preceded by a letter/surrogate, not a delimiter.
var assetPathRe = regexp.MustCompile(`(^|[\s(,:=])("|')/(uploads/|avatars/)`)

// rewriteAssetPaths translates web-style absolute asset paths
// ("/uploads/...", "/avatars/...") to workspace-relative paths
// ("uploads/...", "avatars/...") that resolve via the symlinks created
// by linkAssetDirs. Works on both the main source and dependency content
// (called from resolve.go for dep files too).
func rewriteAssetPaths(src string) string {
	return assetPathRe.ReplaceAllString(src, "$1$2$3")
}

// buildReverseDepMapCtx loads every typst_cache row with non-empty dependencies
// and returns the "who depends on me" map (depID → list of articles that
// import it). Shared between InvalidateDependentsCtx and EnqueueDependentsCtx
// so the two BFS walks don't re-implement the same parse loop.
func buildReverseDepMapCtx(ctx context.Context, dbx *sql.DB) (map[int][]int, error) {
	if dbx == nil {
		return nil, nil
	}
	rows, err := dbx.QueryContext(ctx, `SELECT article_id, dependencies FROM typst_cache WHERE dependencies IS NOT NULL AND dependencies != ''`)
	if err != nil {
		return nil, fmt.Errorf("typst: query dependencies: %w", err)
	}
	reverseDep := make(map[int][]int)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		var aid int
		var depsStr string
		if err := rows.Scan(&aid, &depsStr); err != nil {
			rows.Close()
			return nil, err
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
		return nil, fmt.Errorf("typst: iterate dependency rows: %w", err)
	}
	return reverseDep, nil
}

// Deprecated: Use buildReverseDepMapCtx instead.
func buildReverseDepMap(dbx *sql.DB) (map[int][]int, error) {
	return buildReverseDepMapCtx(context.TODO(), dbx)
}

// transitiveDependents BFS-walks the reverse-dep map from changedID and
// returns the set of articles that (transitively) depend on changedID,
// excluding changedID itself. Shared between InvalidateDependents (which
// deletes their cache rows) and EnqueueDependents (which queues them for
// re-compile). Excluding changedID prevents a circular @import (A imports B,
// B imports A) from re-invalidating the article that just compiled.
func transitiveDependents(reverseDep map[int][]int, changedID int) []int {
	visited := make(map[int]bool)
	var queue []int
	var out []int
	for _, dep := range reverseDep[changedID] {
		if !visited[dep] && dep != changedID {
			visited[dep] = true
			out = append(out, dep)
			queue = append(queue, dep)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range reverseDep[cur] {
			if !visited[dep] && dep != changedID {
				visited[dep] = true
				out = append(out, dep)
				queue = append(queue, dep)
			}
		}
	}
	return out
}

// InvalidateDependents removes cached compilation output for ALL typst
// articles that transitively depend on changedID (via @import). Call this
// after updating or deleting a typst article so readers don't see stale
// compiled output that was built against the old version.
//
// Uses buildReverseDepMap + transitiveDependents so the BFS matches
// EnqueueDependents exactly (the two previously copy-pasted loops had
// subtly different visited-set initialisation).
//
// Deprecated: Use InvalidateDependentsCtx instead.
func (w *Worker) InvalidateDependents(changedID int) error {
	return w.InvalidateDependentsCtx(context.TODO(), changedID)
}

// InvalidateDependentsCtx is the context-aware version of InvalidateDependents.
func (w *Worker) InvalidateDependentsCtx(ctx context.Context, changedID int) error {
	if w == nil || w.db == nil || changedID <= 0 {
		return nil
	}
	reverseDep, err := buildReverseDepMapCtx(ctx, w.db)
	if err != nil {
		return err
	}
	toInvalidate := transitiveDependents(reverseDep, changedID)
	if len(toInvalidate) == 0 {
		return nil
	}

	// Delete cache rows for all invalidated articles.
	args := make([]any, len(toInvalidate))
	for i, id := range toInvalidate {
		args[i] = id
	}
	q := `DELETE FROM typst_cache WHERE article_id IN (` + coredb.Placeholders(len(toInvalidate)) + `)`
	if _, err := w.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("typst: invalidate dependents: %w", err)
	}
	slog.Info("typst: invalidated dependent caches", "changed_id", changedID, "invalidated", toInvalidate)
	return nil
}

// LogCLIAvailability reports whether the typst CLI is on $PATH. main.go
// calls this once at startup so the operator sees a clear "typst not
// installed" warning without paying for it on every import (the old
// package-level init() ran this at import time, surprising test binaries).
func LogCLIAvailability() {
	if p := Path(); p != "" {
		slog.Info("typst CLI found", "path", p)
	} else {
		slog.Warn("typst CLI not found — typst articles will show placeholder")
	}
}
