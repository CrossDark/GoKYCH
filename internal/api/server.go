package api

import (
	"database/sql"

	"gokych/internal/auth/ratelimit"
	"gokych/internal/auth/session"
)

// Server bundles shared dependencies for the API layer.
type Server struct {
	DB       *sql.DB
	sessions *session.Manager
	limiter  *ratelimit.Limiter
	DataDir  string // filesystem path to the runtime data directory
}

// NewServer creates a Server with the given dependencies.
func NewServer(db *sql.DB, sess *session.Manager, limiter *ratelimit.Limiter, dataDir string) *Server {
	return &Server{DB: db, sessions: sess, limiter: limiter, DataDir: dataDir}
}
