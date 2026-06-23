package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	MySQL MySQLConfig
	App   AppConfig
}

// MySQLConfig holds database connection parameters.
type MySQLConfig struct {
	Host     string     `yaml:"host"`
	Port     int        `yaml:"port"`
	User     string     `yaml:"user"`
	Password string     `yaml:"password"`
	Database string     `yaml:"database"`
	Charset  string     `yaml:"charset"`
	Pool     PoolConfig `yaml:"pool"`
}

// PoolConfig holds connection pool settings.
type PoolConfig struct {
	MinSize     int `yaml:"minsize"`
	MaxSize     int `yaml:"maxsize"`
	PoolRecycle int `yaml:"pool_recycle"` // seconds
}

// AppConfig holds application-level settings.
type AppConfig struct {
	Port           int
	GinMode        string
	SessionSecret  string
	AdminUsername  string
	AdminPassword  string
	DataDir        string
	TrustedProxies []string // trusted reverse-proxy CIDRs/IPs for c.ClientIP(); empty = trust none (RemoteAddr only)
}

// mysqlYAML is the YAML file structure (top-level key "mysql").
type mysqlYAML struct {
	MySQL MySQLConfig `yaml:"mysql"`
}

// Load reads configuration from env vars and optional YAML file.
// Priority (highest wins): environment variables > YAML file > defaults.
func Load() Config {
	cfg := Config{}
	cfg.applyDefaults()
	cfg.loadEnvFile()
	cfg.loadYAML()
	cfg.applyEnvOverrides()
	return cfg
}

// applyDefaults sets baseline values.
func (c *Config) applyDefaults() {
	c.MySQL = MySQLConfig{
		Host:     "localhost",
		Port:     3306,
		User:     "gokych",
		Password: "gokych",
		Database: "gokych",
		Charset:  "utf8mb4",
		Pool: PoolConfig{
			MinSize:     2,
			MaxSize:     10,
			PoolRecycle: 3600,
		},
	}
	c.App = AppConfig{
		Port:          8000,
		GinMode:       "debug",
		SessionSecret: "change-me-to-a-random-string",
		AdminUsername: "admin",
		AdminPassword: "admin123",
		DataDir:       "data",
	}
}

// loadEnvFile parses a simple .env file (KEY=VALUE lines, ignores comments and blanks).
func (c *Config) loadEnvFile() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Don't override vars already set (command line / container takes priority).
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}

// loadYAML reads data/settings/db.yaml if it exists.
func (c *Config) loadYAML() {
	path := c.DataRoot() + "/settings/db.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var raw mysqlYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		slog.Warn("failed to parse yaml", "path", path, "err", err)
		return
	}
	// Merge field-by-field instead of c.MySQL = raw.MySQL: a whole-struct
	// assignment would zero out Pool (and any other yaml-omitted field),
	// leaving MinSize=MaxSize=0 and effectively disabling the pool.
	if raw.MySQL.Host != "" {
		c.MySQL.Host = raw.MySQL.Host
	}
	if raw.MySQL.Port != 0 {
		c.MySQL.Port = raw.MySQL.Port
	}
	if raw.MySQL.User != "" {
		c.MySQL.User = raw.MySQL.User
	}
	if raw.MySQL.Password != "" {
		c.MySQL.Password = raw.MySQL.Password
	}
	if raw.MySQL.Database != "" {
		c.MySQL.Database = raw.MySQL.Database
	}
	if raw.MySQL.Charset != "" {
		c.MySQL.Charset = raw.MySQL.Charset
	}
	if raw.MySQL.Pool.MaxSize > 0 {
		c.MySQL.Pool.MaxSize = raw.MySQL.Pool.MaxSize
	}
	if raw.MySQL.Pool.MinSize > 0 {
		c.MySQL.Pool.MinSize = raw.MySQL.Pool.MinSize
	}
	if raw.MySQL.Pool.PoolRecycle > 0 {
		c.MySQL.Pool.PoolRecycle = raw.MySQL.Pool.PoolRecycle
	}
}

// applyEnvOverrides lets environment variables override all settings.
func (c *Config) applyEnvOverrides() {
	// MySQL
	c.MySQL.Host = envOr("DB_HOST", c.MySQL.Host)
	c.MySQL.Port = envIntOr("DB_PORT", c.MySQL.Port)
	c.MySQL.User = envOr("DB_USER", c.MySQL.User)
	c.MySQL.Password = envOr("DB_PASSWORD", c.MySQL.Password)
	c.MySQL.Database = envOr("DB_NAME", c.MySQL.Database)
	c.MySQL.Charset = envOr("DB_CHARSET", c.MySQL.Charset)
	c.MySQL.Pool.MinSize = envIntOr("DB_POOL_MIN", c.MySQL.Pool.MinSize)
	c.MySQL.Pool.MaxSize = envIntOr("DB_POOL_MAX", c.MySQL.Pool.MaxSize)
	c.MySQL.Pool.PoolRecycle = envIntOr("DB_POOL_RECYCLE", c.MySQL.Pool.PoolRecycle)

	// App
	c.App.Port = envIntOr("APP_PORT", c.App.Port)
	c.App.GinMode = envOr("GIN_MODE", c.App.GinMode)
	c.App.SessionSecret = envOr("SESSION_SECRET", c.App.SessionSecret)
	c.App.AdminUsername = envOr("ADMIN_USERNAME", c.App.AdminUsername)
	c.App.AdminPassword = envOr("ADMIN_PASSWORD", c.App.AdminPassword)
	c.App.DataDir = envOr("DATA_DIR", c.App.DataDir)
	c.App.TrustedProxies = envListOr("TRUSTED_PROXIES", c.App.TrustedProxies)
}

// DataRoot returns the absolute path to the data directory.
func (c *Config) DataRoot() string {
	return c.App.DataDir
}

// EnsureDataDirs creates required subdirectories under DataRoot.
func (c *Config) EnsureDataDirs() {
	dirs := []string{
		c.DataRoot(),
		c.DataRoot() + "/settings",
		c.DataRoot() + "/uploads",
		c.DataRoot() + "/avatars",
		c.DataRoot() + "/plugins",
		c.DataRoot() + "/themes",
		c.DataRoot() + "/typst",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			slog.Warn("failed to create data dir", "dir", dir, "err", err)
		}
	}
}

// --- helpers ---

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		slog.Warn("invalid int env, using default", "key", key, "value", v, "default", fallback)
	}
	return fallback
}

// envListOr parses a comma-separated env var into a trimmed string slice.
// Empty/unset yields nil, so callers can distinguish "not configured" from
// a configured empty list.
func envListOr(key string, fallback []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
