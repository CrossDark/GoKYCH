// Package themes loads user-customisable themes from $DATA_DIR/themes/.
//
// A theme is a directory:
//
//	themes/<name>/
//	  theme.yaml          # metadata (name, version, author, description) — required
//	  static/theme.css    # CSS overrides via :root / [data-theme="dark"] vars — required
//
// Themes are loaded read-only at request time; no caching layer for now
// (the on-disk scan is cheap, and admins should see their edits without
// bouncing the server). Add a TTL cache later if the directory grows.
package themes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Theme is the on-disk representation of a single theme.
type Theme struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
	Description string `json:"description,omitempty"`
	// HasCSS reports whether static/theme.css exists and is non-empty.
	HasCSS bool `json:"has_css"`
}

// themeNameRe restricts theme directory names to URL-safe lowercase + dashes.
// The same rule is enforced on the public /api/themes/:name.css endpoint so
// a theme called "../../etc" can't escape its root.
var themeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// themesDir returns <dataDir>/themes.
func themesDir(dataDir string) string { return filepath.Join(dataDir, "themes") }

// ValidateName reports whether name is a safe theme directory name.
func ValidateName(name string) bool { return themeNameRe.MatchString(name) }

// List enumerates every theme directory under dataDir/themes. Subdirectories
// without a valid name or without theme.yaml are silently skipped (logged in
// the caller if they want — we keep List quiet so a stray file doesn't break
// the settings page).
func List(dataDir string) ([]Theme, error) {
	dir := themesDir(dataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("themes.List: read %s: %w", dir, err)
	}
	out := make([]Theme, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !ValidateName(e.Name()) {
			continue
		}
		t, err := readTheme(filepath.Join(dir, e.Name()))
		if err != nil {
			// Bad theme.yaml — skip rather than 500 the whole listing. A
			// real production site might log this; for now the settings
			// page just won't show the broken one.
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get loads a single theme by name. Returns (nil, nil) when the theme doesn't
// exist (so the settings UI can render a 404 / not-found gracefully).
func Get(dataDir, name string) (*Theme, error) {
	if !ValidateName(name) {
		return nil, fmt.Errorf("invalid theme name: %q", name)
	}
	dir := filepath.Join(themesDir(dataDir), name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, nil
	}
	t, err := readTheme(dir)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ReadCSS returns the raw bytes of static/theme.css for the named theme.
// Returns (nil, nil) on a missing theme or missing CSS — the caller should
// decide whether that's a 404 or a graceful skip.
func ReadCSS(dataDir, name string) ([]byte, error) {
	if !ValidateName(name) {
		return nil, fmt.Errorf("invalid theme name: %q", name)
	}
	path := filepath.Join(themesDir(dataDir), name, "static", "theme.css")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("themes.ReadCSS %s: %w", path, err)
	}
	return b, nil
}

// EnsureDefault bootstraps a "sunset" theme directory with a CSS that mirrors
// the built-in :root vars in web/styles/globals.css. Idempotent: only writes
// if the directory is missing or theme.css is empty. Call once at startup.
func EnsureDefault(dataDir string) error {
	dir := filepath.Join(themesDir(dataDir), "sunset")
	if err := os.MkdirAll(filepath.Join(dir, "static"), 0o755); err != nil {
		return err
	}
	// theme.yaml
	yamlPath := filepath.Join(dir, "theme.yaml")
	if _, err := os.Stat(yamlPath); errors.Is(err, os.ErrNotExist) {
		yamlContent := strings.TrimSpace(`
name: Sunset
version: "1.0"
author: GoKYCH
description: 内置暖橘主题 — 浅白底 + 深色卡片
`) + "\n"
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o644); err != nil {
			return err
		}
	}
	// theme.css — keep in sync with web/styles/globals.css :root / dark.
	cssPath := filepath.Join(dir, "static", "theme.css")
	if info, err := os.Stat(cssPath); err != nil || info.Size() == 0 {
		css := strings.TrimSpace(defaultSunsetCSS) + "\n"
		if err := os.WriteFile(cssPath, []byte(css), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// readTheme parses theme.yaml and reports whether static/theme.css exists.
func readTheme(dir string) (Theme, error) {
	yamlPath := filepath.Join(dir, "theme.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return Theme{}, fmt.Errorf("read theme.yaml: %w", err)
	}
	var meta struct {
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Author      string `yaml:"author"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return Theme{}, fmt.Errorf("parse theme.yaml: %w", err)
	}
	name := filepath.Base(dir)
	if meta.Name == "" {
		meta.Name = name
	}
	cssPath := filepath.Join(dir, "static", "theme.css")
	hasCSS := false
	if info, err := os.Stat(cssPath); err == nil && info.Size() > 0 {
		hasCSS = true
	}
	return Theme{
		Name:        name,
		Version:     meta.Version,
		Author:      meta.Author,
		Description: meta.Description,
		HasCSS:      hasCSS,
	}, nil
}

// defaultSunsetCSS is the bundled default theme. Kept in sync with
// web/styles/globals.css :root / [data-theme="dark"] blocks — the layout
// loads this CSS *after* globals.css so the :root overrides take effect.
const defaultSunsetCSS = `/* Sunset — built-in default theme.
 * Mirrors web/styles/globals.css :root / [data-theme="dark"] CSS variables.
 * Drop a custom theme in data/themes/<name>/static/theme.css to override.
 */

:root {
    --bg: #ffffff;
    --bg-card: #ffffff;
    --text: #1a1a1a;
    --text-secondary: #555555;
    --text-muted: #999999;
    --border: #e5e5e5;
    --accent: #3b82f6;
    --accent-hover: #2563eb;
    --code-bg: #f5f5f5;
    --shadow: 0 1px 3px rgba(0, 0, 0, 0.06), 0 1px 2px rgba(0, 0, 0, 0.04);
    --shadow-md: 0 4px 6px rgba(0, 0, 0, 0.05), 0 2px 4px rgba(0, 0, 0, 0.04);
    --radius: 8px;
    --header-bg: rgba(255, 255, 255, 0.92);
}

[data-theme="dark"] {
    --bg: #000000;
    --bg-card: #111111;
    --text: #e0e0e0;
    --text-secondary: #999999;
    --text-muted: #666666;
    --border: #2a2a2a;
    --accent: #60a5fa;
    --accent-hover: #93bcf8;
    --code-bg: #1a1a1a;
    --shadow: 0 1px 3px rgba(0, 0, 0, 0.5), 0 1px 2px rgba(0, 0, 0, 0.4);
    --shadow-md: 0 4px 6px rgba(0, 0, 0, 0.6), 0 2px 4px rgba(0, 0, 0, 0.5);
    --header-bg: rgba(0, 0, 0, 0.92);
}
`
