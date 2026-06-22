package typst

import (
	"context"
	"fmt"
	"log"
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
	bin := Path()
	if bin == "" {
		return "", fmt.Errorf("typst: CLI not found")
	}

	// Limit concurrent compilations.
	compileSem <- struct{}{}
	defer func() { <-compileSem }()

	// Bound the subprocess lifetime so a hang can't pin a goroutine forever.
	ctx, cancel := context.WithTimeout(context.Background(), compileTimeout)
	defer cancel()

	// Write source to a temp .typ file.
	dir, err := os.MkdirTemp("", "gokych-typst-")
	if err != nil {
		return "", fmt.Errorf("typst: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	inputPath := filepath.Join(dir, "input.typ")
	if err := os.WriteFile(inputPath, []byte(source), 0600); err != nil {
		return "", fmt.Errorf("typst: write temp input: %w", err)
	}

	outputPath := filepath.Join(dir, "output.html")

	cmd := exec.CommandContext(ctx, bin, "compile",
		"--format", "html",
		"--features", "html",
		inputPath, outputPath,
	)
	cmd.Env = envWhitelist()
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("typst: compile timed out after %s", compileTimeout)
		}
		return "", fmt.Errorf("typst: compile failed: %w\n%s", err, string(output))
	}

	htmlBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return "", fmt.Errorf("typst: read output: %w", err)
	}

	fullHTML := string(htmlBytes)
	// Extract body content from the full HTML document.
	body := extractBody(fullHTML)
	return body, nil
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
		log.Printf("[typst] found typst CLI at %s", p)
	} else {
		log.Println("[typst] typst CLI not found — typst articles will show placeholder")
	}
}
