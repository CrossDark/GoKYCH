package settings

import (
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Default returns the baseline settings structure. Used by Ensure (initial
// write) and by Load (to fill in any fields the YAML is missing).
func Default() map[string]interface{} {
	return map[string]interface{}{
		"site": map[string]interface{}{
			"title":       "跨越晨昏",
			"subtitle":    "个人网站",
			"description": "",
			"language":    "zh-CN",
			"timezone":    "Asia/Shanghai",
			// Empty by default — older settings.yml shipped "/static/img/logo.png"
			// which is a server-relative path that the EdgeOne-hosted SPA can't
			// resolve (no /static rewrite on the frontend origin). Empty →
			// the frontend renders the 🌅 fallback; admin uploads /uploads/xxx
			// via the file picker to set a real one.
			"logo_path":    "",
			"favicon_path": "",
			"icp_number":   "",
		},
		"appearance": map[string]interface{}{
			"font_family":   "system-ui, -apple-system, sans-serif",
			"primary_color": "#3b82f6",
			"style_theme":   "sunset",
			"theme":         "auto",
		},
		"features": map[string]interface{}{
			"enable_comments":     true,
			"enable_dark_mode":    true,
			"enable_search":       true,
			"enable_tags_sidebar": true,
			"posts_per_page":      10,
			"allow_all_edit":      false,
		},
		// NOTE: there used to be a top-level "social" section here
		// (email/github/twitter). It was moved to per-user fields on the
		// users table (social_email / social_github / social_qq) so each
		// account can have its own links. Legacy settings.yml files may
		// still contain a `social:` block — Load() drops it.
	}
}

// Load reads settings.yml from <dataDir>/settings/ and merges it on top of
// Default(). Missing keys get filled from defaults, so callers always see the
// full structure. If the file is absent or unreadable, defaults are returned
// with a nil error (read failures are LOGGED, not fatal — a freshly-deployed
// site shouldn't 500 just because no admin has hit the settings page yet).
// Parse errors, however, are returned so the admin UI can surface them.
func Load(dataDir string) (map[string]interface{}, error) {
	out := Default()
	path := filepath.Join(dataDir, "settings", "settings.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		slog.Warn("settings.Load: read failed", "path", path, "err", err)
		return out, nil
	}
	var fromYAML map[string]interface{}
	if err := yaml.Unmarshal(data, &fromYAML); err != nil {
		return nil, err
	}
	for section, vals := range fromYAML {
		// Drop legacy sections that have been moved elsewhere. The
		// top-level `social` block lived in settings.yml before social
		// links were relocated to per-user fields; carrying it forward
		// would either leak stale data through /api/site or — once an
		// admin re-saves settings — overwrite a settings.yml that's no
		// longer supposed to contain it. Quietly drop it.
		if section == "social" {
			continue
		}
		m, ok := vals.(map[string]interface{})
		if !ok {
			continue
		}
		base, ok := out[section].(map[string]interface{})
		if !ok {
			out[section] = m
			continue
		}
		for k, v := range m {
			base[k] = v
		}
	}
	return out, nil
}

// Save serialises cfg to settings.yml (overwrites). Caller is expected to
// validate cfg before calling.
func Save(dataDir string, cfg map[string]interface{}) error {
	dir := filepath.Join(dataDir, "settings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.yml"), data, 0o644)
}

// Ensure creates the settings directory and writes default settings.yml if it
// does not already exist.
func Ensure(dataDir string) error {
	dir := filepath.Join(dataDir, "settings")
	path := filepath.Join(dir, "settings.yml")

	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(Default())
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	slog.Info("created default settings.yml", "path", path)
	return nil
}
