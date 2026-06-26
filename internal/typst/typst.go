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
// (e.g. `#import "preview.typ"`) resolve from here, and any image / asset
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
// `#import "preview.typ"`) resolve from there. The input file and the two
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
	inputPath := filepath.Join(workspaceDir, ".input_"+suffix+".typ")
	htmlPath := filepath.Join(workspaceDir, ".output_"+suffix+".html")
	pdfPath := filepath.Join(workspaceDir, ".output_"+suffix+".pdf")
	defer os.Remove(inputPath)
	defer os.Remove(htmlPath)
	defer os.Remove(pdfPath)

	if err := os.WriteFile(inputPath, []byte(source), 0600); err != nil {
		return nil, "", fmt.Errorf("typst: write temp input: %w", err)
	}

	// Compile HTML.
	cmd := exec.CommandContext(ctx, bin, "compile",
		"--format", "html",
		"--features", "html",
		inputPath, htmlPath,
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
	cmdPDF := exec.CommandContext(ctx, bin, "compile", inputPath, pdfPath)
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

// CompileHTMLCached is CompileHTML wrapped with a DB-backed cache keyed on
// articleID (the typst_cache table). On a cache miss it compiles and writes
// the result back. With no DB configured (SetDB not called) it degrades to a
// plain CompileHTML. Cache I/O errors are best-effort: lookups that fail fall
// through to compilation, write failures only log.
func CompileHTMLCached(articleID int, source string) (string, error) {
	if db == nil || articleID <= 0 {
		return CompileHTML(source)
	}
	var html string
	err := db.QueryRow(
		`SELECT html_content FROM typst_cache WHERE article_id = ?`, articleID,
	).Scan(&html)
	if err == nil && html != "" {
		return html, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("typst cache lookup", "article_id", articleID, "err", err)
	}
	body, err := CompileHTML(source)
	if err != nil {
		return "", err
	}
	// pdf_content is NOT NULL in the schema; we only cache HTML here so store
	// an empty blob. ON DUPLICATE KEY UPDATE refreshes html + compiled_at.
	if _, werr := db.Exec(
		`INSERT INTO typst_cache (article_id, html_content, pdf_content)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE html_content = VALUES(html_content), compiled_at = CURRENT_TIMESTAMP`,
		articleID, body, []byte{},
	); werr != nil {
		slog.Error("typst cache write", "article_id", articleID, "err", werr)
	}
	return body, nil
}

// CompilePDFCached returns the cached PDF for articleID, compiling + caching
// on miss. Returns nil bytes (no error) if typst isn't installed or the
// PDF compile failed — the caller should fall back to a 404 / "PDF
// unavailable" message rather than 500'ing the page.
func CompilePDFCached(articleID int, source string) ([]byte, error) {
	if db == nil || articleID <= 0 {
		return CompilePDF(source)
	}
	var cached []byte
	err := db.QueryRow(
		`SELECT pdf_content FROM typst_cache WHERE article_id = ?`, articleID,
	).Scan(&cached)
	if err == nil && len(cached) > 0 {
		return cached, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("typst pdf cache lookup", "article_id", articleID, "err", err)
	}
	pdf, err := CompilePDF(source)
	if err != nil || len(pdf) == 0 {
		return pdf, err
	}
	// Persist the PDF into typst_cache. We use INSERT ... ON DUPLICATE KEY
	// UPDATE so a PDF-first visit (no prior HTML render → no cache row yet)
	// caches just as well as the HTML-first path. The previous code did an
	// UPDATE then a conditional INSERT, but db.Exec returns a nil error
	// even when the UPDATE matched zero rows, so the INSERT fallback never
	// ran and PDFs were recompiled on every request when no HTML view had
	// seeded the row.
	//
	// We only touch pdf_content + compiled_at here so a cached HTML result
	// is preserved when the article was already HTML-rendered.
	if _, werr := db.Exec(
		`INSERT INTO typst_cache (article_id, html_content, pdf_content)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE pdf_content = VALUES(pdf_content), compiled_at = CURRENT_TIMESTAMP`,
		articleID, "", pdf); werr != nil {
		slog.Error("typst pdf cache write", "article_id", articleID, "err", werr)
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
// OOM, etc.). Best-effort — missing files are fine.
func cleanupLeakedInputs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".input_") || strings.HasPrefix(name, ".output_") {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// materializeAssets writes the embedded `assets/*.typ` files into the
// workspace dir. Files that already exist are NOT overwritten (lets users
// customize preview.typ and keep their edits across restarts). Best-effort:
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
