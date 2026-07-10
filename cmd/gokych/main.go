package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"gokych/internal/api"
	"gokych/internal/auth/passkey"
	"gokych/internal/auth/ratelimit"
	"gokych/internal/auth/session"
	"gokych/internal/config"
	"gokych/internal/content"
	coredb "gokych/internal/core/db"
	"gokych/internal/core/logging"
	"gokych/internal/core/metrics"
	"gokych/internal/core/schema"
	"gokych/internal/core/settings"
	"gokych/internal/core/themes"
	"gokych/internal/restart"
	"gokych/internal/typst"
)

// version is populated at build time via -ldflags "-X main.version=vX.Y.Z".
// A dev build (go run / go install without ldflags) leaves it as "dev".
var version = "dev"

func main() {
	// 0. Load configuration.
	cfg := config.Load()

	// 0a. Initialise structured logging (slog) as early as possible so all
	// subsequent startup diagnostics go through it.
	logging.Init(cfg.App.GinMode)
	slog.Info("config loaded",
		"version", version,
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
	siteSettings, err := settings.Load(cfg.App.DataDir)
	if err != nil {
		slog.Warn("failed to load settings.yml, using defaults", "err", err)
		siteSettings = settings.Default()
	}
	siteTitle := "跨越晨昏"
	if site, ok := siteSettings["site"].(map[string]interface{}); ok {
		if t, ok := site["title"].(string); ok && t != "" {
			siteTitle = t
		}
	}
	// Seed built-in themes (sunset/ocean/forest/midnight/paper) so the
	// public /api/themes/:name.css endpoint always has valid themes to
	// serve on first boot. Idempotent — only writes missing/empty files.
	if err := themes.EnsureBuiltins(cfg.App.DataDir); err != nil {
		slog.Warn("failed to seed built-in themes", "err", err)
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
	// SessionCookieDomain scopes the session cookie to a parent domain
	// (e.g. ".kych.net") so the SSR frontend on a sibling subdomain can
	// forward it to the backend; without it, cross-origin SSR sees an
	// anonymous backend response (rating.user_score=null, "登录" shown).
	// Empty in dev / single-host setups.
	sess := session.New(db, cfg.App.SessionSecret, secure, cfg.App.SessionCookieDomain)
	limiter := ratelimit.New()
	m := metrics.New()
	// Construct the shared typst worker. The old API had package-level
	// SetDB / AfterCompileFunc / StartWorker / StopWorker; consolidating
	// them onto a Worker struct removes the global mutable state (race-free
	// startup, testable with isolated DBs) and lets the same instance be
	// injected into the API Server and the content layer.
	typstW := typst.NewWorker(db)
	// typst.SetWorkspaceDir pins the typst project root to an absolute path
	// under DataDir. Without this, typst would fall back to a cwd-relative
	// "data/typst" which breaks when the binary isn't run from the project
	// root (e.g. systemd, Docker, tests).
	typst.SetWorkspaceDir(cfg.App.DataDir + "/typst")
	// Link uploads/ and avatars/ into the typst workspace so typst
	// articles can #image("uploads/foo.png") and #image("avatars/bar.jpg").
	typst.SetAssetsDirs(cfg.App.DataDir+"/uploads", cfg.App.DataDir+"/avatars")
	// One-shot CLI availability log (replaces the old package-init() side
	// effect that fired on every import, surprising test binaries).
	typst.LogCLIAvailability()

	// Hook typst compilation success → sync post-processed HTML into
	// articles.rendered_html so subsequent reads hit the DB cache directly
	// without re-running typst or the post-processor.
	typstW.SetAfterCompile(func(ctx context.Context, articleID int, htmlBody string, depIDs []int) {
		if err := content.UpdateTypstHTMLCtx(ctx, db, articleID, htmlBody); err != nil {
			slog.Warn("main: failed to sync typst rendered_html", "article_id", articleID, "err", err)
		}
		// Re-render any non-typst dependents whose cache was invalidated.
		for _, did := range depIDs {
			var dtype, dslug string
			if err := db.QueryRowContext(ctx, `SELECT type, slug FROM articles WHERE id = ?`, did).Scan(&dtype, &dslug); err == nil {
				if dtype != "typst" {
					if da, err := content.GetArticleCtx(ctx, db, dtype, dslug); err == nil {
						_ = content.RenderAndSaveCtx(ctx, db, typstW, da)
					}
				}
			}
		}
	})

	// Start the async typst compilation worker pool (2 workers = up to 2
	// concurrent compilations, further bounded by compileSem at 4 in the
	// typst package itself). This decouples HTTP request latency from
	// compilation time — articles are saved immediately and compiled in
	// the background.
	typstW.StartWorker(2)
	defer typstW.StopWorker()

	// Warm the article render cache at startup (non-blocking batch).
	// Pre-populates articles.rendered_html for existing articles so the
	// first visitor after deploy gets instant HTML.
	go func() {
		content.WarmCacheCtx(context.Background(), db, typstW, 50)
	}()
	srv := api.NewServer(db, sess, limiter, m, cfg.App.DataDir, cfg.App.TrustedProxies)
	srv.Typst = typstW
	// PublicURL is the absolute base URL the backend is reachable at
	// from the public internet — used to build absolute /uploads/*
	// responses for cross-origin frontends (Cloudflare Pages). Empty in
	// dev, where Next.js rewrites /uploads/* → backend.
	srv.PublicURL = cfg.App.PublicURL
	// Inject build version for admin panel display / self-update checks.
	srv.Version = version
	// CORS allowlist: comma-separated env, empty in dev. When empty the
	// CORS middleware is a no-op and only same-origin requests succeed —
	// fine for the dev shell, but production must set this or the CF
	// Pages frontend can't talk to the API at all.
	srv.CORSAllowedOrigins = cfg.App.CORSAllowedOrigins
	if len(srv.CORSAllowedOrigins) > 0 {
		slog.Info("cors allowed origins", "origins", srv.CORSAllowedOrigins)
	}
	if srv.PublicURL != "" {
		slog.Info("public url", "url", srv.PublicURL)
	}

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
		rpid, origin := passkey.NormalizeDomain(cfg.App.WebAuthnDomain)
		if rpid == "" {
			slog.Warn("passkey disabled: APP_DOMAIN could not be parsed", "value", cfg.App.WebAuthnDomain)
		} else {
			// WebAuthn origin validation is byte-exact — scheme, host AND
			// port must each match clientDataJSON.origin. A bare
			// APP_DOMAIN=localhost yields the primary origin http://localhost,
			// but the user is almost certainly reaching the site through
			// the Next.js dev server on :3000 (or the Go backend on :8000),
			// which would make every passkey ceremony fail with
			// "Error validating origin". Build the accepted-origins list
			// from the primary origin PLUS same-scheme same-host variants
			// with the common dev ports, plus the http↔https peer for
			// localhost (so a misconfigured http vs https APP_DOMAIN still
			// works locally). Production domains don't get extra variants
			// — the operator is expected to set APP_DOMAIN to the real
			// origin (port included if non-default), which already lives
			// in the primary slot.
			origins := passkey.BuildOrigins(origin, rpid)
			srv.ConfigureWebAuthn(rpid, siteTitle, origins)
			slog.Info("passkey configured", "rpid", rpid, "origins", origins)
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

	// Inject the self-update restart hook. Must happen AFTER httpSrv is
	// declared (the closure captures it) and BEFORE the server starts
	// accepting requests (so an admin hitting /api/admin/update/apply
	// immediately after boot finds the hook in place).
	api.SetRestartFunc(func() error {
		// Called from the update handler after the new binary is in
		// place. We return immediately and schedule the actual restart
		// 2s later so the HTTP response can flush to the client first.
		time.AfterFunc(2*time.Second, func() {
			// Close long-lived resources BEFORE handing control to
			// either the systemd path (which will SIGTERM us) or the
			// syscall.Exec path (which won't run defers). Both paths
			// need the DB pool / typst worker quiet so the new
			// process can start cleanly.
			db.Close()
			typstW.StopWorker()
			switch restart.PickStrategy() {
			case restart.StrategySystemd:
				// Spawn `sudo systemctl restart gokych.service` in a
				// detached process group. The command will SIGTERM
				// us, the graceful-shutdown signal handler in main
				// below runs, and the new instance starts.
				slog.Info("update: restarting via systemd")
				if err := restart.SystemdRestart("gokych.service"); err != nil {
					slog.Error("update: systemd restart failed, falling back to exec", "err", err)
					exePath, _ := os.Executable()
					_ = restart.ExecRestart(exePath, os.Args, os.Environ())
				}
			default:
				// No systemd (dev / docker / manual run). In-place
				// exec keeps the same PID so the parent shell
				// doesn't notice anything happened. This is the
				// pre-systemd behaviour; preserved for backwards
				// compatibility.
				slog.Info("update: restarting via in-process exec")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpSrv.Shutdown(ctx); err != nil {
					slog.Error("update: graceful shutdown error, forcing restart", "err", err)
				}
				exePath, err := os.Executable()
				if err != nil {
					slog.Error("update: cannot find executable path", "err", err)
					return
				}
				_ = restart.ExecRestart(exePath, os.Args, os.Environ())
			}
		})
		return nil
	})

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
	// Stop the typst worker first so in-flight compilations finish or
	// abort cleanly before we close the DB connection.
	typstW.StopWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}
