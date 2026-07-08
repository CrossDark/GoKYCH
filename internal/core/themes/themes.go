// Package themes loads user-customisable themes from $DATA_DIR/themes/.
//
// A theme is a directory:
//
//	themes/<name>/
//	  theme.yaml          # metadata (name, version, author, description) + settings schema — required
//	  static/theme.css    # CSS overrides via :root / [data-theme="dark"] vars — required
//	  static/effects/...  # optional JS / images / fonts bundled with the theme
//
// Built-in themes live as plain YAML/CSS files in the builtin/ subdirectory
// (embedded into the binary via go:embed) and are extracted to data/themes/
// on startup via EnsureBuiltins(). User-uploaded themes live alongside them
// in the same data/themes/ directory. Built-in themes carry a .builtin flag
// so the UI can prevent deletion; user themes can be freely uploaded and
// removed.
package themes

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed all:builtin
var builtinFS embed.FS

// Theme is the on-disk representation of a single theme.
type Theme struct {
	Name        string              `json:"name"`
	Version     string              `json:"version,omitempty"`
	Author      string              `json:"author,omitempty"`
	Description string              `json:"description,omitempty"`
	HasCSS      bool                `json:"has_css"`
	Builtin     bool                `json:"builtin"`
	UpdatedAt   *time.Time          `json:"updated_at,omitempty"`
	// Settings is the SCHEMA declared in the theme's theme.yaml `settings:`
	// block. Each entry describes one configurable knob (type / label /
	// options / default). The schema is read at theme-load time; the EFFECTIVE
	// values (admin overrides) live in the theme_settings DB table and are
	// fetched separately by the API. Empty slice means the theme has no
	// configurable settings.
	Settings []SettingDefinition `json:"settings,omitempty"`
}

// SettingDefinition describes one configurable knob declared in a theme's
// theme.yaml. The `default` value is whatever YAML parses — string for
// `text`/`image`/`select` keys, int for `range` (we coerce to float64 in
// the admin UI to handle Step values that don't divide evenly).
type SettingDefinition struct {
	Key     string   `json:"key"               yaml:"key"`
	Type    string   `json:"type"              yaml:"type"` // select | range | text | image
	Label   string   `json:"label"             yaml:"label"`
	Options []string `json:"options,omitempty" yaml:"options,omitempty"`
	Min     *int     `json:"min,omitempty"     yaml:"min,omitempty"`
	Max     *int     `json:"max,omitempty"     yaml:"max,omitempty"`
	Step    *int     `json:"step,omitempty"    yaml:"step,omitempty"`
	Default any      `json:"default,omitempty" yaml:"default,omitempty"`
	Hint    string   `json:"hint,omitempty"    yaml:"hint,omitempty"`
}

var themeNameRe = regexp.MustCompile(`^[a-z0-9\x{4e00}-\x{9fff}][a-z0-9\x{4e00}-\x{9fff}_-]{0,63}$`)

func themesDir(dataDir string) string { return filepath.Join(dataDir, "themes") }

func ValidateName(name string) bool { return themeNameRe.MatchString(name) }

func List(dataDir string) ([]Theme, error) {
	dir := themesDir(dataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("themes.List: read %s: %w", dir, err)
	}
	builtins := builtinThemeNames()
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
			continue
		}
		t.Builtin = builtins[t.Name]
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

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
	t.Builtin = builtinThemeNames()[name]
	return &t, nil
}

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

// ReadAsset returns the bytes of any file inside the theme's static/
// subdirectory plus its MIME type (whitelisted by extension). Path
// traversal is rejected by checking that the cleaned path stays inside
// the theme's static/ directory. Returns (nil, "", nil) if the file
// doesn't exist.
//
// The route is mounted at /api/themes/<name>/assets/*filepath — gin's
// *filepath wildcard captures the segment after "assets/" (no leading
// slash), so we manually prepend "static/" to map it onto the on-disk
// layout (<theme-dir>/static/<wildcard>).
//
// Used by the glass theme's particles.js, and designed to be reusable
// for any future theme that ships its own JS / images / fonts.
func ReadAsset(dataDir, name, relPath string) ([]byte, string, error) {
	if !ValidateName(name) {
		return nil, "", fmt.Errorf("invalid theme name: %q", name)
	}
	cleaned := strings.TrimPrefix(relPath, "/")
	if cleaned == "" || strings.HasPrefix(cleaned, ".") || strings.Contains(cleaned, "..") {
		return nil, "", fmt.Errorf("invalid asset path: %q", relPath)
	}
	// Treat the wildcard as a path under static/.
	fsRel := "static/" + cleaned
	ext := strings.ToLower(filepath.Ext(fsRel))
	mime := mimeByExt(ext)
	if mime == "" {
		return nil, "", fmt.Errorf("unsupported asset type: %s", ext)
	}
	// Resolve the final FS path and verify it stays inside the theme dir.
	themeDir := filepath.Join(themesDir(dataDir), name)
	absBase, _ := filepath.Abs(themeDir)
	target := filepath.Join(themeDir, filepath.FromSlash(fsRel))
	absPath, err := filepath.Abs(target)
	if err != nil || !strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) {
		return nil, "", fmt.Errorf("asset path escapes theme dir")
	}
	b, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("themes.ReadAsset %s: %w", target, err)
	}
	return b, mime, nil
}

// mimeByExt maps file extensions to a small set of whitelisted MIME types
// for theme-bundled static assets. Returning "" causes ReadAsset to error,
// which prevents arbitrary file types from being served through the
// /api/themes/:name/assets/ endpoint.
func mimeByExt(ext string) string {
	switch ext {
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return ""
	}
}

// Delete removes a user-uploaded theme directory. Returns an error if the
// theme is built-in, doesn't exist, or the name is invalid.
func Delete(dataDir, name string) error {
	if !ValidateName(name) {
		return fmt.Errorf("invalid theme name: %q", name)
	}
	if builtinThemeNames()[name] {
		return fmt.Errorf("cannot delete built-in theme %q", name)
	}
	dir := filepath.Join(themesDir(dataDir), name)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("theme %q not found", name)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("theme path %q is not a directory", name)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove theme dir: %w", err)
	}
	return nil
}

// InstallFromZip extracts a theme zip archive into data/themes/<name>.
// The archive must contain a top-level theme.yaml and static/theme.css
// (either at the archive root or inside a single top-level folder).
// If a user theme with the same name already exists it is overwritten;
// built-in themes cannot be overwritten.
func InstallFromZip(dataDir string, zipBytes []byte) (*Theme, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	var themeName string
	var yamlContent []byte
	var cssContent []byte
	var foundFiles = make(map[string][]byte)

	stripPrefix := detectZipPrefix(reader.File)

	for _, f := range reader.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "__MACOSX/") || strings.HasSuffix(name, "/") {
			continue
		}
		if stripPrefix != "" {
			if !strings.HasPrefix(name, stripPrefix) {
				continue
			}
			name = strings.TrimPrefix(name, stripPrefix)
		}
		if name == "" {
			continue
		}
		if strings.Contains(name, "..") {
			return nil, fmt.Errorf("invalid path in zip: %s", f.Name)
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, 2<<20))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
		}

		lName := strings.ToLower(name)
		switch {
		case lName == "theme.yaml" || lName == "theme.yml":
			yamlContent = content
		case strings.HasSuffix(lName, "static/theme.css"):
			cssContent = content
		}
		foundFiles[name] = content
	}

	if len(yamlContent) == 0 {
		return nil, errors.New("theme.yaml not found in archive")
	}
	if len(cssContent) == 0 {
		return nil, errors.New("static/theme.css not found in archive")
	}

	var meta struct {
		Name        string              `yaml:"name"`
		Version     string              `yaml:"version"`
		Author      string              `yaml:"author"`
		Description string              `yaml:"description"`
		Settings    []SettingDefinition `yaml:"settings"`
	}
	if err := yaml.Unmarshal(yamlContent, &meta); err != nil {
		return nil, fmt.Errorf("parse theme.yaml: %w", err)
	}

	if meta.Name == "" {
		return nil, errors.New("theme.yaml missing required 'name' field")
	}

	slug := slugify(meta.Name)
	if !ValidateName(slug) {
		return nil, fmt.Errorf("theme name %q produces invalid slug %q", meta.Name, slug)
	}
	themeName = slug

	if builtinThemeNames()[themeName] {
		return nil, fmt.Errorf("cannot overwrite built-in theme %q", themeName)
	}

	dir := filepath.Join(themesDir(dataDir), themeName)
	staticDir := filepath.Join(dir, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir theme dir: %w", err)
	}

	yamlPath := filepath.Join(dir, "theme.yaml")
	if err := os.WriteFile(yamlPath, yamlContent, 0o644); err != nil {
		return nil, fmt.Errorf("write theme.yaml: %w", err)
	}

	cssPath := filepath.Join(staticDir, "theme.css")
	if err := os.WriteFile(cssPath, cssContent, 0o644); err != nil {
		return nil, fmt.Errorf("write theme.css: %w", err)
	}

	for name, content := range foundFiles {
		if name == "theme.yaml" || name == "theme.yml" || strings.HasSuffix(name, "static/theme.css") {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)+string(os.PathSeparator)) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			slog.Warn("theme install: mkdir extra file", "path", target, "err", err)
			continue
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			slog.Warn("theme install: write extra file", "path", target, "err", err)
		}
	}

	t, err := readTheme(dir)
	if err != nil {
		return nil, err
	}
	t.Builtin = false
	return &t, nil
}

// InstallFromCSS creates a simple theme from raw CSS bytes plus a display name.
// Used for the "upload CSS file" shortcut that doesn't require zipping.
func InstallFromCSS(dataDir, displayName string, cssBytes []byte) (*Theme, error) {
	if displayName == "" {
		return nil, errors.New("theme name required")
	}
	slug := slugify(displayName)
	if !ValidateName(slug) {
		return nil, fmt.Errorf("theme name %q produces invalid slug %q", displayName, slug)
	}
	if builtinThemeNames()[slug] {
		return nil, fmt.Errorf("cannot overwrite built-in theme %q", slug)
	}
	if len(cssBytes) == 0 {
		return nil, errors.New("CSS content is empty")
	}

	dir := filepath.Join(themesDir(dataDir), slug)
	staticDir := filepath.Join(dir, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		return nil, err
	}

	yamlContent := fmt.Sprintf("name: %s\nversion: \"1.0\"\nauthor: (uploaded)\ndescription: 上传的自定义主题\n", displayName)
	if err := os.WriteFile(filepath.Join(dir, "theme.yaml"), []byte(yamlContent), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(staticDir, "theme.css"), cssBytes, 0o644); err != nil {
		return nil, err
	}

	t, err := readTheme(dir)
	if err != nil {
		return nil, err
	}
	t.Builtin = false
	return &t, nil
}

func detectZipPrefix(files []*zip.File) string {
	var prefix string
	for _, f := range files {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 2 && parts[1] != "" {
			if prefix == "" {
				prefix = parts[0] + "/"
			} else if parts[0]+"/" != prefix {
				return ""
			}
		} else {
			return ""
		}
	}
	return prefix
}

func slugify(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '\u4e00' && r <= '\u9fff':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	out = regexp.MustCompile(`-+`).ReplaceAllString(out, "-")
	return out
}

func readTheme(dir string) (Theme, error) {
	yamlPath := filepath.Join(dir, "theme.yaml")
	yamlInfo, err := os.Stat(yamlPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			yamlPath = filepath.Join(dir, "theme.yml")
			yamlInfo, err = os.Stat(yamlPath)
		}
		if err != nil {
			return Theme{}, fmt.Errorf("stat theme.yaml: %w", err)
		}
	}
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return Theme{}, fmt.Errorf("read theme.yaml: %w", err)
	}
	var meta struct {
		Name        string              `yaml:"name"`
		Version     string              `yaml:"version"`
		Author      string              `yaml:"author"`
		Description string              `yaml:"description"`
		Settings    []SettingDefinition `yaml:"settings"`
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
	var updatedAt time.Time = yamlInfo.ModTime()
	if info, err := os.Stat(cssPath); err == nil && info.Size() > 0 {
		hasCSS = true
		if info.ModTime().After(updatedAt) {
			updatedAt = info.ModTime()
		}
	}
	return Theme{
		Name:        name,
		Version:     meta.Version,
		Author:      meta.Author,
		Description: meta.Description,
		HasCSS:      hasCSS,
		Settings:    meta.Settings,
		UpdatedAt:   &updatedAt,
	}, nil
}

// ── Built-in themes ──────────────────────────────────────────────────

// builtinThemeNames returns the set of theme slugs shipped with GoKYCH.
// It discovers them by reading the embedded builtin/ directory at init time,
// so adding a new subdirectory under builtin/ automatically registers it.
func builtinThemeNames() map[string]bool {
	names := make(map[string]bool)
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return names
	}
	for _, e := range entries {
		if e.IsDir() && ValidateName(e.Name()) {
			names[e.Name()] = true
		}
	}
	return names
}

// EnsureBuiltins extracts all built-in themes from the embedded filesystem
// into dataDir/themes/. Idempotent — only writes files when missing or empty,
// so user edits to built-in theme CSS in the data directory are preserved
// across restarts.
func EnsureBuiltins(dataDir string) error {
	base := themesDir(dataDir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}

	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return fmt.Errorf("read builtin themes dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		if !ValidateName(slug) {
			continue
		}

		srcDir := "builtin/" + slug
		dstDir := filepath.Join(base, slug)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dstDir, err)
		}

		// Walk every file in the embedded theme directory and copy it to
		// the data directory, skipping files that already exist and are
		// non-empty (so user customisations survive restart).
		err := fs.WalkDir(builtinFS, srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			// path starts with srcDir + "/" (e.g. "builtin/sunset/static/theme.css")
			// Strip the srcDir prefix to get the relative path inside the theme.
			rel := strings.TrimPrefix(path, srcDir+"/")
			if rel == path {
				return nil
			}
			// Convert to OS path separator for the target filesystem.
			targetPath := filepath.Join(dstDir, filepath.FromSlash(rel))

			// Idempotency: skip existing non-empty files.
			if info, stErr := os.Stat(targetPath); stErr == nil && info.Size() > 0 {
				return nil
			}

			content, err := fs.ReadFile(builtinFS, path)
			if err != nil {
				return fmt.Errorf("read embedded %s: %w", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(targetPath, content, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", targetPath, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("extract built-in theme %s: %w", slug, err)
		}
	}
	return nil
}

// EnsureDefault is kept for backwards compatibility — it now calls EnsureBuiltins.
// Deprecated: Use EnsureBuiltins instead.
func EnsureDefault(dataDir string) error {
	return EnsureBuiltins(dataDir)
}

// ── Per-theme settings store (P10) ────────────────────────────────────
//
// A theme's theme.yaml `settings:` block declares a SCHEMA — each entry
// has a key, type (select | range | text | image), label, options/range
// bounds, and a default value. The schema is read at theme-load time and
// shipped to the admin UI so it can render matching form controls.
//
// The EFFECTIVE values — what the admin actually picked, overriding the
// schema default — live in the `theme_settings` table. The admin's choice
// takes precedence over the schema default; if no row exists the schema
// default is used.
//
// This split lets theme authors evolve the schema (add a new knob, widen
// a range) without losing existing admin overrides: we just key by
// (theme_name, key) and keep the stored value as-is across upgrades.

// GetSettingsValues reads all stored (theme_name, key) -> value rows for
// the given theme. Missing rows are simply absent from the returned map —
// callers fall back to the schema default. An empty map + nil error
// means "no overrides set" (totally valid; the admin hasn't customised
// the theme yet).
func GetSettingsValues(db *sql.DB, themeName string) (map[string]string, error) {
	if !ValidateName(themeName) {
		return nil, fmt.Errorf("invalid theme name: %q", themeName)
	}
	rows, err := db.Query(
		"SELECT `key`, value FROM theme_settings WHERE theme_name = ?",
		themeName,
	)
	if err != nil {
		return nil, fmt.Errorf("theme_settings select: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("theme_settings scan: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSettingsValues upserts the given values for a theme. Keys absent
// from the schema are rejected; keys present in the schema but missing
// from `values` are NOT deleted (the admin might just not have touched
// them — the row stays, the schema default is irrelevant for that key).
//
// Returns the list of keys that were rejected (with a reason) so the API
// handler can return a useful 400 response.
func SetSettingsValues(db *sql.DB, themeName string, schema []SettingDefinition, values map[string]string) ([]string, error) {
	if !ValidateName(themeName) {
		return nil, fmt.Errorf("invalid theme name: %q", themeName)
	}
	// Build a schema lookup for fast validation.
	type schEnt struct {
		def SettingDefinition
	}
	byKey := make(map[string]schEnt, len(schema))
	for _, s := range schema {
		byKey[s.Key] = schEnt{def: s}
	}
	// First pass: validate every incoming value against its schema entry.
	var rejects []string
	cleaned := make(map[string]string, len(values))
	for k, v := range values {
		e, ok := byKey[k]
		if !ok {
			rejects = append(rejects, fmt.Sprintf("key %q is not declared in this theme's settings schema", k))
			continue
		}
		if reason := validateSettingValue(e.def, v); reason != "" {
			rejects = append(rejects, fmt.Sprintf("%s: %s", k, reason))
			continue
		}
		cleaned[k] = v
	}
	if len(rejects) > 0 {
		return rejects, fmt.Errorf("invalid setting values")
	}
	if len(cleaned) == 0 {
		return nil, nil
	}
	// Second pass: upsert each cleaned value in its own statement.
	// (theme_settings is small — at most a handful of rows per theme —
	// so a single-row per insert is fine; the row count is bounded by
	// the schema length which is essentially a constant.)
	for k, v := range cleaned {
		if _, err := db.Exec(
			"INSERT INTO theme_settings (theme_name, `key`, value) VALUES (?, ?, ?) "+
				"ON DUPLICATE KEY UPDATE value = VALUES(value)",
			themeName, k, v,
		); err != nil {
			return rejects, fmt.Errorf("theme_settings upsert %s: %w", k, err)
		}
	}
	return nil, nil
}

// validateSettingValue returns "" if v is acceptable for the schema entry,
// or a short reason string if not. Caller is expected to surface the
// reason in the API response.
func validateSettingValue(s SettingDefinition, v string) string {
	switch s.Type {
	case "select":
		for _, o := range s.Options {
			if v == o {
				return ""
			}
		}
		return fmt.Sprintf("must be one of %v, got %q", s.Options, v)
	case "range":
		// Range values are ints (Step-bounded). We accept decimals in the
		// YAML and round to int at the UI layer; here we parse as int.
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Sprintf("must be an integer, got %q", v)
		}
		if s.Min != nil && n < *s.Min {
			return fmt.Sprintf("must be >= %d, got %d", *s.Min, n)
		}
		if s.Max != nil && n > *s.Max {
			return fmt.Sprintf("must be <= %d, got %d", *s.Max, n)
		}
		return ""
	case "text", "image":
		// Free-form strings. Cap length to keep one bad row from filling
		// the TEXT column with megabytes of garbage.
		if len(v) > 4096 {
			return fmt.Sprintf("too long (%d bytes, max 4096)", len(v))
		}
		return ""
	default:
		return fmt.Sprintf("unknown setting type %q (theme schema bug)", s.Type)
	}
}
