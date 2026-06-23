package api

import (
	"database/sql"

	"gokych/internal/auth/ratelimit"
	"gokych/internal/auth/session"
	"gokych/internal/core/metrics"
)

// Server bundles shared dependencies for the API layer.
type Server struct {
	DB             *sql.DB
	sessions       *session.Manager
	limiter        *ratelimit.Limiter
	Metrics        *metrics.Metrics
	DataDir        string   // filesystem path to the runtime data directory
	trustedProxies []string // trusted reverse-proxy CIDRs/IPs; empty = trust none
}

// NewServer creates a Server with the given dependencies.
func NewServer(db *sql.DB, sess *session.Manager, limiter *ratelimit.Limiter, m *metrics.Metrics, dataDir string, trustedProxies []string) *Server {
	return &Server{
		DB:             db,
		sessions:       sess,
		limiter:        limiter,
		Metrics:        m,
		DataDir:        dataDir,
		trustedProxies: trustedProxies,
	}
}
