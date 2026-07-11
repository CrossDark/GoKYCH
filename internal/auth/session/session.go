package session

import (
	"context"
	"database/sql"
	"log/slog"
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

// HasUserID reports whether the request's session has a user_id key —
// used by loadUserMiddleware to distinguish "no session at all" from
// "session present but invalidated" (the latter is the signature of an
// admin-triggered force-logout / password rotation).
func (m *Manager) HasUserID(r *http.Request) bool {
	s, err := m.Session(r)
	if err != nil {
		return false
	}
	uidRaw, ok := s.Values["user_id"]
	if !ok {
		return false
	}
	uid, ok := uidRaw.(int64)
	return ok && uid != 0
}

// ClearInvalidated wipes the cookie for a session that was previously
// authenticated but has just been forcibly invalidated
// (session_invalidated_at bumped past the session's login_time). Called
// by loadUserMiddleware so the browser stops sending the now-dead
// session id on subsequent requests.
func (m *Manager) ClearInvalidated(r *http.Request, w http.ResponseWriter) {
	s, err := m.Session(r)
	if err != nil {
		return
	}
	s.Options.MaxAge = -1
	delete(s.Values, "user_id")
	delete(s.Values, "username")
	delete(s.Values, "login_time")
	delete(s.Values, "last_activity")
	delete(s.Values, "csrf_token")
	if err := s.Save(r, w); err != nil {
		slog.Error("session clear invalidated", "err", err)
	}
}

// CurrentUserCtx resolves the authenticated user for the request, enforcing the
// 24h idle timeout and the per-user session_invalidated_at "kick-out"
// timestamp. On expiry, forced-logout, or anonymous access it returns nil
// (and clears stale session values if expired). The resolved *user.User is
// stashed in the request context for downstream handlers.
func (m *Manager) CurrentUserCtx(ctx context.Context, r *http.Request) (*user.User, error) {
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

	u, err := user.GetByIDCtx(ctx, m.db, int(uid))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if u == nil {
		return nil, nil
	}

	// Per-user kick-out: if the user's session_invalidated_at is set and
	// is later than this session's login_time, the session was created
	// before the user's credentials were rotated / they were forcibly
	// logged out — drop the session. We do NOT mutate the session here;
	// the caller is responsible for clearing it (loadUserMiddleware
	// sets s.Options.MaxAge = -1 in that case so the cookie is wiped
	// on the next response).
	//
	// Both sides are int64 Unix timestamps, so the compare is exact and
	// doesn't depend on MySQL DATETIME ↔ Go time.Time timezone
	// interpretation (which is the bug this column was changed to
	// avoid — see schema.go for the long version).
	loginTimeRaw, _ := s.Values["login_time"].(int64)
	if u.SessionInvalidatedAt > 0 && loginTimeRaw > 0 {
		if u.SessionInvalidatedAt > loginTimeRaw {
			return nil, nil
		}
	}

	return u, nil
}

// Deprecated: Use CurrentUserCtx instead.
func (m *Manager) CurrentUser(r *http.Request) (*user.User, error) {
	return m.CurrentUserCtx(context.TODO(), r)
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
