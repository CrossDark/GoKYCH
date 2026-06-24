package typst

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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
// and once for PDF — and returns both. The two compilations share the same
// temp dir + semaphore slot + timeout, so the second invocation is much
// cheaper than the first (typst is incremental, the source is already on
// disk in the OS page cache).
func compileBoth(source string) (pdf []byte, html string, err error) {
	bin := Path()
	if bin == "" {
		return nil, "", fmt.Errorf("typst: CLI not found")
	}

	// Limit concurrent compilations. Both HTML and PDF invocations share
	// one slot so a request for "give me both" doesn't bypass the cap.
	compileSem <- struct{}{}
	defer func() { <-compileSem }()

	// Bound the subprocess lifetime so a hang can't pin a goroutine forever.
	ctx, cancel := context.WithTimeout(context.Background(), compileTimeout)
	defer cancel()

	// Write source to a temp .typ file.
	dir, err := os.MkdirTemp("", "gokych-typst-")
	if err != nil {
		return nil, "", fmt.Errorf("typst: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	inputPath := filepath.Join(dir, "input.typ")
	if err := os.WriteFile(inputPath, []byte(source), 0600); err != nil {
		return nil, "", fmt.Errorf("typst: write temp input: %w", err)
	}

	// Compile HTML.
	htmlPath := filepath.Join(dir, "output.html")
	cmd := exec.CommandContext(ctx, bin, "compile",
		"--format", "html",
		"--features", "html",
		inputPath, htmlPath,
	)
	cmd.Env = envWhitelist()
	cmd.Dir = dir
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
	pdfPath := filepath.Join(dir, "output.pdf")
	cmdPDF := exec.CommandContext(ctx, bin, "compile", inputPath, pdfPath)
	cmdPDF.Env = envWhitelist()
	cmdPDF.Dir = dir
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
	// Persist alongside whatever's already there for html_content — this
	// is a separate path from CompileHTMLCached so the two formats can be
	// compiled at different times (e.g. user opened the article before
	// the PDF button was ever clicked).
	if _, werr := db.Exec(
		`UPDATE typst_cache SET pdf_content = ?, compiled_at = CURRENT_TIMESTAMP
		 WHERE article_id = ?`, pdf, articleID); werr != nil {
		// No row yet (this article has never been HTML-rendered)? Insert a
		// fresh one with both fields empty except the PDF.
		if _, werr2 := db.Exec(
			`INSERT INTO typst_cache (article_id, html_content, pdf_content)
			 VALUES (?, '', ?)
			 ON DUPLICATE KEY UPDATE pdf_content = VALUES(pdf_content), compiled_at = CURRENT_TIMESTAMP`,
			articleID, pdf); werr2 != nil {
			slog.Error("typst pdf cache write", "article_id", articleID, "err", werr, "err2", werr2)
		}
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

func init() {
	if p := Path(); p != "" {
		slog.Info("typst CLI found", "path", p)
	} else {
		slog.Warn("typst CLI not found — typst articles will show placeholder")
	}
}
