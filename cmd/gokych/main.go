package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
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
	// SessionCookieDomain scopes the session cookie to a parent domain
	// (e.g. ".kych.net") so the SSR frontend on a sibling subdomain can
	// forward it to the backend; without it, cross-origin SSR sees an
	// anonymous backend response (rating.user_score=null, "登录" shown).
	// Empty in dev / single-host setups.
	sess := session.New(db, cfg.App.SessionSecret, secure, cfg.App.SessionCookieDomain)
	limiter := ratelimit.New()
	m := metrics.New()
	// typst.SetDB lets typst.CompileHTMLCached consult typst_cache.
	typst.SetDB(db)
	// typst.SetWorkspaceDir pins the typst project root to an absolute path
	// under DataDir. Without this, typst would fall back to a cwd-relative
	// "data/typst" which breaks when the binary isn't run from the project
	// root (e.g. systemd, Docker, tests).
	typst.SetWorkspaceDir(cfg.App.DataDir + "/typst")
	// Start the async typst compilation worker pool (2 workers = up to 2
	// concurrent compilations, further bounded by compileSem at 4 in the
	// typst package itself). This decouples HTTP request latency from
	// compilation time — articles are saved immediately and compiled in
	// the background.
	typst.StartWorker(db, 2)
	defer typst.StopWorker()
	srv := api.NewServer(db, sess, limiter, m, cfg.App.DataDir, cfg.App.TrustedProxies)
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
		rpid, origin := normalizeWebAuthnDomain(cfg.App.WebAuthnDomain)
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
			origins := buildWebAuthnOrigins(origin, rpid)
			srv.ConfigureWebAuthn(rpid, "跨越晨昏", origins)
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
			slog.Info("update: restarting service...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := httpSrv.Shutdown(ctx); err != nil {
				slog.Error("update: graceful shutdown error, forcing restart", "err", err)
			}
			// Re-exec the binary at the same path with the same args/env.
			// syscall.Exec replaces the current process image; PID stays
			// the same so systemd tracks it as the continuous main PID.
			exePath, err := os.Executable()
			if err != nil {
				slog.Error("update: cannot find executable path", "err", err)
				return
			}
			db.Close()
			typst.StopWorker()
			if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
				slog.Error("update: exec failed", "err", err)
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
	typst.StopWorker()
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

// buildWebAuthnOrigins expands a single primary origin (derived from
// APP_DOMAIN) into the full set the webauthn library will accept during
// FinishRegistration / FinishDiscoverableLogin. WebAuthn's origin check is
// byte-exact: scheme, host AND port must each equal
// clientDataJSON.origin. A bare APP_DOMAIN like "localhost" yields the
// primary origin "http://localhost", yet the user is almost always reaching
// the site through the Next.js dev server on :3000 (or the API on :8000) —
// with only the primary in the list, every ceremony fails with
// "Error validating origin".
//
// Strategy:
//   - Always include the primary origin as-is (production deployments set
//     APP_DOMAIN to the real origin and rely on this slot matching).
//   - For the special "localhost" host, add http(s)://localhost with the
//     common dev ports (3000, 8000, 8080) so the default dev setup works
//     without the operator having to remember "APP_DOMAIN=localhost:3000".
//   - For any other host, additionally add the same scheme + host with
//     ports 3000/8000/8080 too. These are unlikely in production (the
//     operator would set the real origin up front) and the cost is tiny;
//     a misconfigured :8000-only deployment still works. We deliberately
//     DON'T add cross-scheme (http://example.com when primary is
//     https://example.com) variants for non-localhost hosts — that would
//     silently allow plaintext-origin access on a production HTTPS site.
//   - For localhost only, also add the http↔https counterpart so a dev
//     box that mis-set APP_DOMAIN=https://localhost still works.
//
// Deduplicated and order-stable so the startup log line is readable.
func buildWebAuthnOrigins(primary, rpid string) []string {
	scheme := "http"
	if strings.HasPrefix(primary, "https://") {
		scheme = "https"
	}
	// Dev ports we tolerate as alternative origins for the same host. Kept
	// short and explicit — these are the ports the project's own
	// docker-compose / next dev default to.
	devPorts := []string{"3000", "8000", "8080"}

	out := []string{primary}
	// localhost is special: it's never reachable over the public internet,
	// always HTTP in dev, and a dev box might be TLS-terminated locally, so
	// we don't gate the cross-scheme variant for it. For other hosts we
	// keep scheme strict to avoid weakening production HTTPS sites.
	schemes := []string{scheme}
	if rpid == "localhost" {
		if scheme == "http" {
			schemes = append(schemes, "https")
		} else {
			schemes = append(schemes, "http")
		}
	}
	seen := map[string]bool{primary: true}
	add := func(o string) {
		if o == "" || seen[o] {
			return
		}
		seen[o] = true
		out = append(out, o)
	}
	for _, sch := range schemes {
		// bare-host variant (no port) — already covered by primary when
		// APP_DOMAIN had no port; harmless to re-add once via dedup.
		add(sch + "://" + rpid)
		for _, p := range devPorts {
			add(sch + "://" + rpid + ":" + p)
		}
	}
	return out
}
