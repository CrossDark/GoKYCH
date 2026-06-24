package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/api"
	"gokych/internal/auth/ratelimit"
	"gokych/internal/auth/session"
	"gokych/internal/config"
	coredb "gokych/internal/core/db"
	"gokych/internal/core/logging"
	"gokych/internal/core/metrics"
	"gokych/internal/core/schema"
	"gokych/internal/core/settings"
	"gokych/internal/core/themes"
	"gokych/internal/typst"
)

func main() {
	// 0. Load configuration.
	cfg := config.Load()

	// 0a. Initialise structured logging (slog) as early as possible so all
	// subsequent startup diagnostics go through it.
	logging.Init(cfg.App.GinMode)
	slog.Info("config loaded",
		"db_host", cfg.MySQL.Host,
		"db_port", cfg.MySQL.Port,
		"db_name", cfg.MySQL.Database,
		"port", cfg.App.Port,
		"gin_mode", cfg.App.GinMode,
	)

	// 0b. In release mode, refuse to start with the default SessionSecret —
	// that would let any attacker forge session cookies. Developers running
	// locally keep working with the default (debug mode).
	if cfg.App.GinMode == "release" && cfg.App.SessionSecret == "change-me-to-a-random-string" {
		log.Fatalf("[main] refusing to start in release mode with the default SESSION_SECRET; set a strong random value via env or config")
	}

	// 2. Ensure data directories and default settings exist.
	cfg.EnsureDataDirs()
	if err := settings.Ensure(cfg.App.DataDir); err != nil {
		slog.Warn("failed to create default settings.yml", "err", err)
	}
	// Seed the built-in "sunset" theme so the public /api/themes/:name.css
	// endpoint always has at least one valid theme to serve on first boot.
	if err := themes.EnsureDefault(cfg.App.DataDir); err != nil {
		slog.Warn("failed to seed default theme", "err", err)
	}

	// 3. Initialize database connection pool.
	db, err := coredb.Init(cfg)
	if err != nil {
		log.Fatalf("[main] failed to connect database: %v", err)
	}
	defer db.Close()

	// 4. Initialize schema (create tables if not exist).
	if err := schema.Init(db); err != nil {
		log.Fatalf("[main] failed to initialize schema: %v", err)
	}

	// 5. Seed default admin.
	schema.SeedAdmin(db, cfg.App.AdminUsername, cfg.App.AdminPassword)

	// 6. Set up session manager + rate limiter + metrics.
	secure := cfg.App.GinMode == "release"
	sess := session.New(db, cfg.App.SessionSecret, secure)
	limiter := ratelimit.New()
	m := metrics.New()
	// typst.SetDB lets typst.CompileHTMLCached consult typst_cache.
	typst.SetDB(db)
	srv := api.NewServer(db, sess, limiter, m, cfg.App.DataDir, cfg.App.TrustedProxies)

	// Configure WebAuthn relying-party identity. RPID is the bare domain
	// ("localhost" or your public hostname) — browsers compare it against
	// the calling origin's domain to scope passkeys. RPOrigin is the full
	// https://host the auth flows run from. APP_DOMAIN can be either form
	// ("example.com" or "https://example.com"); we normalise to the
	// origin for the lib.
	if cfg.App.WebAuthnDomain != "" {
		// Normalise APP_DOMAIN into the (rpid, origin) pair the WebAuthn
		// lib needs. Accept any of these forms:
		//   "example.com"            → rpid "example.com",   origin "http://example.com"
		//   "localhost:3000"         → rpid "localhost",     origin "http://localhost:3000"
		//   "https://example.com"    → rpid "example.com",   origin "https://example.com"
		//   "http://localhost:3000"  → rpid "localhost",     origin "http://localhost:3000"
		// RPID must be the bare host (no scheme/port): browsers compare it
		// against the calling origin's registrable domain. RPOrigin is the
		// full scheme://host[:port] the auth flows run from — it must
		// match the browser's window.origin byte-for-byte, port included,
		// so the docker-compose frontend (http://localhost:3000) works.
		rpid, origin := normalizeWebAuthnDomain(cfg.App.WebAuthnDomain)
		if rpid == "" {
			slog.Warn("passkey disabled: APP_DOMAIN could not be parsed", "value", cfg.App.WebAuthnDomain)
		} else {
			srv.ConfigureWebAuthn(rpid, "跨越晨昏", origin)
			slog.Info("passkey configured", "rpid", rpid, "origin", origin)
		}
	} else {
		slog.Warn("passkey disabled: APP_DOMAIN not set; passkey endpoints will 503")
	}

	// 7. Set up Gin. gin.Recovery() stays here; access logging + request id
	// are handled by the per-request middleware registered in srv.Setup, so
	// we don't double-log via gin.Logger().
	gin.SetMode(cfg.App.GinMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// 8. Register routes.
	srv.Setup(r)

	// 8b. Static resources. Mounted AFTER Setup so the security-headers
	// middleware (registered inside Setup) still wraps these handlers.
	// gin.Static wraps http.FileServer + http.Dir, which sanitises ".."
	// segments — path-traversal attempts resolve to 404.
	//
	// We stamp a long Cache-Control header on /uploads/* and /avatars/*
	// since uploaded files have content-hashed filenames (sha256[:24] +
	// ext); re-uploads land at a new URL, so there's no stale-content
	// hazard.
	uploadDir := cfg.App.DataDir + "/uploads"
	avatarDir := cfg.App.DataDir + "/avatars"
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/uploads/") ||
			strings.HasPrefix(c.Request.URL.Path, "/avatars/") {
			c.Header("Cache-Control", "public, max-age=3600")
		}
		c.Next()
	})
	r.Static("/uploads", uploadDir)
	r.Static("/avatars", avatarDir)

	// 9. Start server with graceful shutdown.
	addr := fmt.Sprintf(":%d", cfg.App.Port)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("server starting", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[main] server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}

// normalizeWebAuthnDomain turns an APP_DOMAIN value (bare host, host:port,
// or full origin with a scheme) into the (rpid, origin) pair the WebAuthn
// library expects. Returns ("", "") when the input can't be parsed so the
// caller can disable passkey rather than start with a broken RPID.
func normalizeWebAuthnDomain(domain string) (rpid, origin string) {
	d := strings.TrimSpace(domain)
	if d == "" {
		return "", ""
	}
	// url.Parse needs a scheme to populate Host; otherwise it puts the
	// whole "host:port" into Path. Inject http:// when the caller omits
	// one (dev default is a bare domain / host:port).
	withScheme := d
	if !strings.Contains(d, "://") {
		withScheme = "http://" + d
	}
	u, err := url.Parse(withScheme)
	if err != nil || u.Host == "" {
		return "", ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "http"
	}
	// Hostname() strips the port — that's exactly the bare RPID we want.
	return u.Hostname(), scheme + "://" + u.Host
}
