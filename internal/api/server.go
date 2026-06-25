package api

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"gokych/internal/auth/ratelimit"
	"gokych/internal/auth/session"
	"gokych/internal/core/metrics"
)

// errPasskeyNotConfigured is returned when the server is started without
// ConfigureWebAuthn (or with an empty RPID). The passkey endpoints surface
// a 503 in that case; non-passkey endpoints are unaffected.
var errPasskeyNotConfigured = errors.New("passkey not configured: APP_DOMAIN unset or empty")

// Server bundles shared dependencies for the API layer.
type Server struct {
	DB             *sql.DB
	sessions       *session.Manager
	limiter        *ratelimit.Limiter
	Metrics        *metrics.Metrics
	DataDir        string   // filesystem path to the runtime data directory
	trustedProxies []string // trusted reverse-proxy CIDRs/IPs; empty = trust none

	// WebAuthn configuration: the relying-party ID (domain) and the
	// list of accepted origins. Passkey origin validation is
	// byte-exact (scheme + host + port must all match clientDataJSON.origin),
	// so a single bare http://localhost <Origin> setting rejects any
	// request coming from the Next.js dev server on :3000. We keep an
	// explicit slice populated by ConfigureWebAuthn instead: the primary
	// origin derived from APP_DOMAIN plus convenience local-dev variants
	// for the same host so the salt-of-the-earth `localhost` setup "just
	// works" without the operator having to set APP_DOMAIN=localhost:3000.
	// Production deployments still match via their primary origin.
	webAuthnRPID      string
	webAuthnRPName    string
	webAuthnRPOrigins []string
	webAuthnOnce      sync.Once
	webAuthn          *webauthn.WebAuthn
	webAuthnErr       error
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

// ConfigureWebAuthn stores the RPID / display name / accepted origins to use
// when serving passkey challenges. RPID is the bare registrable domain (no
// scheme/port); origins are the full scheme://host[:port] values the
// browser may report as window.origin during a WebAuthn ceremony — the match
// is byte-exact (port included), so callers must list each origin the site is
// reachable from. main.go expands a single APP_DOMAIN into this slice and
// folds in a few localhost variants for convenience.
func (s *Server) ConfigureWebAuthn(rpid, rpName string, origins []string) {
	s.webAuthnRPID = rpid
	s.webAuthnRPName = rpName
	s.webAuthnRPOrigins = origins
}

// webAuthnInstance returns the lazily-built *webauthn.WebAuthn. Safe to
// call from any handler; the first one to call wins, the rest see the
// same value.
func (s *Server) webAuthnInstance() (*webauthn.WebAuthn, error) {
	s.webAuthnOnce.Do(func() {
		if s.webAuthnRPID == "" {
			s.webAuthnErr = errPasskeyNotConfigured
			return
		}
		origins := s.webAuthnRPOrigins
		if len(origins) == 0 {
			s.webAuthnErr = errPasskeyNotConfigured
			return
		}
		w, err := webauthn.New(&webauthn.Config{
			RPID:          s.webAuthnRPID,
			RPDisplayName: s.webAuthnRPName,
			RPOrigins:     origins,
			AuthenticatorSelection: protocol.AuthenticatorSelection{
				UserVerification: protocol.VerificationPreferred,
			},
		})
		if err != nil {
			s.webAuthnErr = err
			return
		}
		s.webAuthn = w
	})
	return s.webAuthn, s.webAuthnErr
}
