package settings

import (
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// defaultSettings returns the baseline settings structure.
func Default() map[string]interface{} {
	return map[string]interface{}{
		"site": map[string]interface{}{
			"title":        "跨越晨昏",
			"subtitle":     "个人网站",
			"description":  "",
			"language":     "zh-CN",
			"timezone":     "Asia/Shanghai",
			"logo_path":    "/static/img/logo.png",
			"favicon_path": "/static/img/favicon.ico",
			"icp_number":   "",
		},
		"appearance": map[string]interface{}{
			"font_family":  "system-ui, -apple-system, sans-serif",
			"primary_color": "#3b82f6",
			"style_theme":   "sunset",
			"theme":         "auto",
		},
		"features": map[string]interface{}{
			"enable_comments":    true,
			"enable_dark_mode":   true,
			"enable_search":      true,
			"enable_tags_sidebar": true,
			"posts_per_page":     10,
		},
		"social": map[string]interface{}{
			"email":  "",
			"github": "",
			"twitter": "",
		},
	}
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

	log.Printf("[settings] created default settings.yml at %s", path)
	return nil
}
