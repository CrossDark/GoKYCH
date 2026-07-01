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
)

//go:embed assets/*.typ
var embeddedTypstFS embed.FS

const (
	compileTimeout = 30 * time.Second
	maxConcurrent  = 4
)

// compileSem bounds the number of concurrent typst compilations to avoid
// fork-bomb / disk exhaustion under load.
var compileSem = make(chan struct{}, maxConcurrent)

// db holds an optional DB connection used by CompileHTMLCached to look up /
// store the typst_cache table. nil = caching disabled (degrades to plain
// CompileHTML).
var db *sql.DB

// SetDB wires a database connection for compile-result caching. Must be
// called once at startup (after the pool is ready) for the cache to take effect.
func SetDB(d *sql.DB) { db = d }

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
var (
	workspaceDirOnce sync.Once
	workspaceDir     string
)

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
func CompileHTML(source string) (string, error) {
	_, html, err := compileBoth(source)
	return html, err
}

// CompilePDF compiles a typst source string to PDF bytes. Same timeout +
// concurrency guards as CompileHTML. Note: this calls the typst CLI a second
// time on the same source — typst doesn't support emitting both formats in
// one invocation, so PDF users pay for one extra compile (the compileHTML
// fast-path doesn't share work). The result is cached separately via
// CompilePDFCached so subsequent PDF requests are free.
func CompilePDF(source string) ([]byte, error) {
	pdf, _, err := compileBoth(source)
	return pdf, err
}

// compileBoth runs the typst CLI twice on the same source — once for HTML
// and once for PDF — and returns both. Both invocations run with
// `cmd.Dir = workspaceDir` so relative imports in the source (e.g.
// `#import "template.typ"`) resolve from there. The input file and the two
// output files use a per-invocation unique prefix (UnixNano + PID) so the
// semaphore's maxConcurrent goroutines can run without trampling each
// other.
func compileBoth(source string) (pdf []byte, html string, err error) {
	bin := Path()
	if bin == "" {
		return nil, "", fmt.Errorf("typst: CLI not found")
	}
	// Trigger lazy workspace setup (mkdir + materialize + leak cleanup) on
	// first compile. Tests that don't call CompileHTML never hit this path.
	ensureWorkspace()

	// Limit concurrent compilations. Both HTML and PDF invocations share
	// one slot so a request for "give me both" doesn't bypass the cap.
	compileSem <- struct{}{}
	defer func() { <-compileSem }()

	// Bound the subprocess lifetime so a hang can't pin a goroutine forever.
	ctx, cancel := context.WithTimeout(context.Background(), compileTimeout)
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
		return nil, "", fmt.Errorf("typst: write temp input: %w", err)
	}

	// Compile HTML.
	// Note: cmd.Dir is set to workspaceDir, so we pass just the basename
	// to typst to avoid path duplication (workspaceDir/workspaceDir/...).
	cmd := exec.CommandContext(ctx, bin, "compile",
		"--format", "html",
		"--features", "html",
		inputName, htmlName,
	)
	cmd.Env = envWhitelist()
	cmd.Dir = workspaceDir
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", fmt.Errorf("typst: compile timed out after %s", compileTimeout)
		}
		return nil, "", fmt.Errorf("typst: html compile failed: %w\n%s", err, string(output))
	}
	htmlBytes, err := os.ReadFile(htmlPath)
	if err != nil {
		return nil, "", fmt.Errorf("typst: read html output: %w", err)
	}
	html = extractBody(string(htmlBytes))

	// Compile PDF. typst's default format IS pdf, so we just don't pass
	// --format. The CLI will still pick up the timeout via ctx.
	cmdPDF := exec.CommandContext(ctx, bin, "compile", inputName, pdfName)
	cmdPDF.Env = envWhitelist()
	cmdPDF.Dir = workspaceDir
	if output, err := cmdPDF.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", fmt.Errorf("typst: pdf compile timed out after %s", compileTimeout)
		}
		// Don't fail the whole call if only the PDF compile failed — HTML
		// is already useful. Caller can detect via empty pdf slice.
		slog.Warn("typst pdf compile failed", "err", err, "output", string(output))
		return nil, html, nil
	}
	pdf, err = os.ReadFile(pdfPath)
	if err != nil {
		return nil, html, fmt.Errorf("typst: read pdf output: %w", err)
	}
	return pdf, html, nil
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
// Requires SetDB to have been called. If typst isn't installed the error
// surfaces as a clear "CLI not found" message, which the admin handler
// translates into a 400 with a hint.
func CompileAndCache(articleID int, source string) error {
	if db == nil {
		return errors.New("typst: db not configured (SetDB not called)")
	}
	if articleID <= 0 {
		return errors.New("typst: invalid article id")
	}
	pdf, html, err := compileBoth(source)
	if err != nil {
		return err
	}
	if html == "" {
		return errors.New("typst: HTML compile produced empty output")
	}
	if len(pdf) == 0 {
		return errors.New("typst: PDF compile produced empty output (typst CLI failed or syntax error)")
	}
	if _, err := db.Exec(
		`INSERT INTO typst_cache (article_id, html_content, pdf_content)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   html_content = VALUES(html_content),
		   pdf_content  = VALUES(pdf_content),
		   compiled_at  = CURRENT_TIMESTAMP`,
		articleID, html, pdf,
	); err != nil {
		return fmt.Errorf("typst: cache write failed: %w", err)
	}
	return nil
}

// CompileHTMLCached is the READ-ONLY cache lookup for articleID. It does
// NOT fall back to CompileHTML — a missing row is a hard miss that the
// renderer must surface as a "pending compile" placeholder, because the
// compile happens at publish time (see CompileAndCache). If you need
// unconditional compilation, call CompileHTML(source) directly.
//
// The db == nil branch is kept for tests / partial setups where the typst
// package is used without a database — in that case the function still
// returns a clear "no cache available" error rather than silently
// recompiling (the previous behaviour masked the bug where a fresh DB
// would re-fork typst on every request).
func CompileHTMLCached(articleID int, source string) (string, error) {
	if db == nil || articleID <= 0 {
		return "", fmt.Errorf("typst: cache unavailable (db configured=%t, articleID=%d)", db != nil, articleID)
	}
	var html string
	err := db.QueryRow(
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
func CompilePDFCached(articleID int, source string) ([]byte, error) {
	if db == nil || articleID <= 0 {
		return nil, fmt.Errorf("typst: cache unavailable (db configured=%t, articleID=%d)", db != nil, articleID)
	}
	var pdf []byte
	err := db.QueryRow(
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
// `.output_*.pdf` files left over from a previous crash (process kill,
// OOM, etc.). Logs the outcome so stale files don't go unnoticed.
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
		if strings.HasPrefix(name, ".input_") || strings.HasPrefix(name, ".output_") {
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

func init() {
	// Note: workspace setup (mkdir / materialize / leak cleanup) is
	// intentionally NOT done here. `go test` changes cwd to the package
	// dir, so a `data/typst` default would pollute the project source
	// tree. Instead, SetWorkspaceDir (called from main.go) or the lazy
	// ensureWorkspace in compileBoth handles setup. The CLI path lookup
	// is a fast read from $PATH and is safe to log here.
	if p := Path(); p != "" {
		slog.Info("typst CLI found", "path", p)
	} else {
		slog.Warn("typst CLI not found — typst articles will show placeholder")
	}
}
