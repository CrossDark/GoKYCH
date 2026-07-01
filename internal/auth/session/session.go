package session

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gorilla/sessions"

	"gokych/internal/auth/user"
)

const (
	cookieName    = "gokych_session"
	sessionMaxAge = 24 * 60 * 60 // 24h idle timeout (seconds)
)

// Manager wraps a gorilla/sessions cookie store and the DB for user lookups.
type Manager struct {
	store *sessions.CookieStore
	db    *sql.DB
}

// New creates a Manager. secret is the session signing key; secure controls
// the cookie Secure flag (set true for HTTPS production). domain is the
// cookie Domain attribute (e.g. ".example.com" to share the session across
// all subdomains of the deployment). Empty = bind cookie to the origin
// that set it (api host only); the SSR frontend on a sibling subdomain
// then can't see it and backend-side CurrentUser returns nil for those
// requests.
//
// SameSite follows the Secure flag: HTTPS production needs `None` so a
// cross-origin SSR fetch (eo.kych.net → api.kych.net) still carries the
// session cookie, but Chrome refuses `SameSite=None` on a non-secure
// (HTTP) cookie — dev login would 403. In dev we fall back to `Lax`,
// which works for same-origin requests and is the Chrome-default
// acceptance rule.
func New(db *sql.DB, secret string, secure bool, domain string) *Manager {
	s := sessions.NewCookieStore([]byte(secret))
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	s.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		Secure:   secure,
		Domain:   domain,
		SameSite: sameSite,
	}
	return &Manager{store: s, db: db}
}

// Session returns the gorilla session for a request (always non-nil).
func (m *Manager) Session(r *http.Request) (*sessions.Session, error) {
	return m.store.Get(r, cookieName)
}

// Save persists session changes to the response.
func (m *Manager) Save(r *http.Request, w http.ResponseWriter, s *sessions.Session) error {
	return s.Save(r, w)
}

// Login establishes a session for the given user id (session-fixation safe).
func (m *Manager) Login(w http.ResponseWriter, r *http.Request, userID int, username string) error {
	s, err := m.Session(r)
	if err != nil {
		return err
	}
	s.Options.MaxAge = sessionMaxAge         // reset (clear() sets -1)
	s.Values = map[interface{}]interface{}{} // wipe pre-login state (fixation protection)
	s.Values["user_id"] = int64(userID)
	s.Values["username"] = username
	s.Values["login_time"] = time.Now().Unix()
	s.Values["last_activity"] = time.Now().Unix()
	tok, err := generateToken()
	if err != nil {
		return err
	}
	s.Values["csrf_token"] = tok
	return s.Save(r, w)
}

// Logout clears the session.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) error {
	s, err := m.Session(r)
	if err != nil {
		return err
	}
	s.Options.MaxAge = -1
	delete(s.Values, "user_id")
	delete(s.Values, "username")
	delete(s.Values, "login_time")
	delete(s.Values, "last_activity")
	delete(s.Values, "csrf_token")
	return s.Save(r, w)
}

// CurrentUser resolves the authenticated user for the request, enforcing the
// 24h idle timeout. On expiry or anonymous access it returns nil (and clears
// stale session values if expired). The resolved *user.User is stashed in the
// request context for downstream handlers.
func (m *Manager) CurrentUser(r *http.Request) (*user.User, error) {
	s, err := m.Session(r)
	if err != nil {
		return nil, err
	}
	uidRaw, ok := s.Values["user_id"]
	if !ok {
		return nil, nil
	}
	uid, ok := uidRaw.(int64)
	if !ok || uid == 0 {
		return nil, nil
	}

	// Idle timeout check.
	lastRaw, _ := s.Values["last_activity"].(int64)
	if time.Now().Unix()-lastRaw > sessionMaxAge {
		delete(s.Values, "user_id")
		delete(s.Values, "last_activity")
		delete(s.Values, "csrf_token")
		return nil, nil
	}

	// Refresh idle timer (sliding window).
	s.Values["last_activity"] = time.Now().Unix()
	// Note: saving on every request is handled by middleware after handler runs.

	u, err := user.GetByID(m.db, int(uid))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// CSRFToken returns the session's CSRF token, generating one if absent.
func (m *Manager) CSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	s, err := m.Session(r)
	if err != nil {
		return "", err
	}
	if tok, ok := s.Values["csrf_token"].(string); ok && tok != "" {
		return tok, nil
	}
	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	s.Values["csrf_token"] = tok
	return tok, s.Save(r, w)
}

// VerifyCSRF checks the provided token against the session's token (constant-time).
func (m *Manager) VerifyCSRF(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	s, err := m.Session(r)
	if err != nil {
		return false
	}
	expected, ok := s.Values["csrf_token"].(string)
	if !ok || expected == "" {
		return false
	}
	return constantTimeCompare(expected, token)
}

// PersistSession saves any session mutations accumulated during the request
// (e.g. last_activity refresh). Called by middleware after the handler.
func (m *Manager) PersistSession(w http.ResponseWriter, r *http.Request) error {
	s, err := m.Session(r)
	if err != nil {
		return err
	}
	if s.IsNew {
		return nil
	}
	return s.Save(r, w)
}
