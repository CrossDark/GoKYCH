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
	// allowed origins. Filled in by main.go from APP_DOMAIN + the
	// request's Origin header. WebAuthn itself is constructed lazily
	// on first passkey request so a misconfigured domain doesn't break
	// the rest of the API at startup.
	webAuthnRPID     string
	webAuthnRPName   string
	webAuthnRPOrigin string
	webAuthnOnce     sync.Once
	webAuthn         *webauthn.WebAuthn
	webAuthnErr      error
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

// ConfigureWebAuthn stores the RPID / display name / origin to use when
// serving passkey challenges. RPID is the bare domain (no scheme/port);
// origin is the full https://host. Called from main.go after we know
// the bind address.
func (s *Server) ConfigureWebAuthn(rpid, rpName, origin string) {
	s.webAuthnRPID = rpid
	s.webAuthnRPName = rpName
	s.webAuthnRPOrigin = origin
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
		w, err := webauthn.New(&webauthn.Config{
			RPID:          s.webAuthnRPID,
			RPDisplayName: s.webAuthnRPName,
			RPOrigins:     []string{s.webAuthnRPOrigin},
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
