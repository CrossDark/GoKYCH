package main

import (
	"context"
	"fmt"
	"log"
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
	"gokych/internal/core/schema"
	"gokych/internal/core/settings"
)

func main() {
	// 1. Load configuration.
	cfg := config.Load()
	log.Printf("[main] config: db=%s:%d/%s, port=%d, gin_mode=%s",
		cfg.MySQL.Host, cfg.MySQL.Port, cfg.MySQL.Database,
		cfg.App.Port, cfg.App.GinMode)

	// 2. Ensure data directories and default settings exist.
	cfg.EnsureDataDirs()
	if err := settings.Ensure(cfg.App.DataDir); err != nil {
		log.Printf("[main] warning: failed to create default settings.yml: %v", err)
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

	// 6. Set up session manager + rate limiter.
	secure := cfg.App.GinMode == "release"
	sess := session.New(db, cfg.App.SessionSecret, secure)
	limiter := ratelimit.New()
	srv := api.NewServer(db, sess, limiter, cfg.App.DataDir)

	// 7. Set up Gin.
	gin.SetMode(cfg.App.GinMode)
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// 8. Register routes.
	srv.Setup(r)

	// 9. Start server with graceful shutdown.
	addr := fmt.Sprintf(":%d", cfg.App.Port)
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		log.Printf("[main] server starting on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[main] server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[main] shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("[main] shutdown error: %v", err)
	}
	log.Println("[main] server stopped")
}
