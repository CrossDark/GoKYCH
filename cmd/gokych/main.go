package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
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
	// gin.StaticFS uses http.FileServer under the hood, which sanitises
	// ".." segments — path-traversal attempts resolve to 404.
	//
	// We also stamp a long Cache-Control header on the responses since
	// uploaded files have content-hashed filenames (sha256[:24] + ext) so
	// re-uploads replace the file at a different URL — there's no stale-
	// content problem to worry about.
	uploadDir := cfg.App.DataDir + "/uploads"
	avatarDir := cfg.App.DataDir + "/avatars"
	cacheableStatic := func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=3600")
		c.Next()
	}
	r.GET("/uploads/*filepath", cacheableStatic, gin.WrapH(http.FileServer(http.Dir(uploadDir))))
	r.GET("/avatars/*filepath", cacheableStatic, gin.WrapH(http.FileServer(http.Dir(avatarDir))))

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
