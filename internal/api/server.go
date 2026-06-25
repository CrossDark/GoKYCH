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

	// PublicURL is the absolute base URL the backend is reachable at
	// from the public internet (e.g. "https://api.example.com"). It
	// prefixes paths returned in API responses — currently only
	// /uploads/<file> — so cross-origin frontends (Cloudflare Pages,
	// separate admin tools) get a URL the browser can fetch directly
	// without same-origin rewrites. Empty in dev (the Next.js
	// /uploads/* rewrite covers it).
	PublicURL string

	// CORSAllowedOrigins is the whitelist of origins that may make
	// credentialed cross-origin requests to the API. Each entry must
	// include scheme + host[:port] (e.g. "https://gokych.example.com");
	// wildcards are not allowed when credentials are sent. When nil or
	// empty the CORS middleware is effectively a no-op — only same-
	// origin requests succeed.
	CORSAllowedOrigins []string

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
				// ResidentKey=preferred (not required) lets any modern
				// authenticator — Touch ID, Windows Hello, hardware keys —
				// create a client-side discoverable credential. We need
				// that because the login flow is discoverable (no username
				// prompt); without a resident credential the browser can't
				// even surface the passkey in the login chooser.
				// VerificationPreferred covers the common case (Touch ID
				// / Hello verify the user; PIN-required roaming keys still
				// work without forcing UV).
				ResidentKey:      protocol.ResidentKeyRequirementPreferred,
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
