package typst

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

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

// CompileHTML compiles a typst source string to HTML body content.
// Uses a temporary file for input and output.
func CompileHTML(source string) (string, error) {
	bin := Path()
	if bin == "" {
		return "", fmt.Errorf("typst: CLI not found")
	}

	// Write source to a temp .typ file.
	dir, err := os.MkdirTemp("", "gokych-typst-")
	if err != nil {
		return "", fmt.Errorf("typst: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	inputPath := filepath.Join(dir, "input.typ")
	if err := os.WriteFile(inputPath, []byte(source), 0644); err != nil {
		return "", fmt.Errorf("typst: write temp input: %w", err)
	}

	outputPath := filepath.Join(dir, "output.html")

	cmd := exec.Command(bin, "compile",
		"--format", "html",
		"--features", "html",
		inputPath, outputPath,
	)
	cmd.Env = os.Environ()
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
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
