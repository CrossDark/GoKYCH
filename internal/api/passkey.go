package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"gokych/internal/auth/passkey"
	"gokych/internal/auth/user"
)

// maxCredentialNameLen caps the user-supplied label for a new passkey.
const maxCredentialNameLen = 64

// ── Registration (requires login) ──────────────────────────────────────

// POST /api/auth/passkey/register/begin
//
// Start a passkey registration. Returns a JSON-encoded
// protocol.CredentialCreation (the client passes it to
// navigator.credentials.create()). The session is updated with the
// challenge so /finish can verify it.
func (s *Server) beginPasskeyRegistration(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后再添加 Passkey。"})
		return
	}
	wa, err := s.webAuthnInstance()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Passkey 未配置。"})
		return
	}
	pkUser, err := passkey.LoadUser(s.DB, u.ID)
	if err != nil {
		slog.Error("beginPasskeyRegistration: load user", "user_id", u.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载用户失败。"})
		return
	}
	options, sessionData, err := wa.BeginRegistration(pkUser)
	if err != nil {
		slog.Error("beginPasskeyRegistration: begin", "user_id", u.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法开始 Passkey 注册。"})
		return
	}
	// Stash the SessionData so Finish can pick it up. We marshal it as JSON
	// because gorilla/sessions values must be strings (no binary blobs).
	if err := s.setSessionValueJSON(c, "webauthn_reg", sessionData); err != nil {
		slog.Error("beginPasskeyRegistration: store session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法保存 challenge。"})
		return
	}
	c.JSON(http.StatusOK, options)
}

// POST /api/auth/passkey/register/finish { name, credential }
//
// Complete the registration: parse the client-side PublicKeyCredential,
// verify the attestation against the in-flight challenge, and persist
// the new credential.
func (s *Server) finishPasskeyRegistration(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	wa, err := s.webAuthnInstance()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Passkey 未配置。"})
		return
	}
	var in struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || len(in.Credential) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(in.Credential))
	if err != nil {
		slog.Error("finishPasskeyRegistration: parse", "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的注册响应。"})
		return
	}
	sessionData, err := s.popSessionValueJSON(c, "webauthn_reg")
	if err != nil || sessionData == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "注册会话已过期，请刷新页面后重试。"})
		return
	}
	pkUser, err := passkey.LoadUser(s.DB, u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载用户失败。"})
		return
	}
	cred, err := wa.CreateCredential(pkUser, *sessionData, parsed)
	if err != nil {
		slog.Error("finishPasskeyRegistration: create", "user_id", u.ID, "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Passkey 注册失败：" + err.Error()})
		return
	}
	name := in.Name
	if name == "" {
		name = "未命名 Passkey"
	}
	if len(name) > maxCredentialNameLen {
		name = name[:maxCredentialNameLen]
	}
	if err := passkey.SaveCredential(s.DB, u.ID, name, cred); err != nil {
		slog.Error("finishPasskeyRegistration: save", "user_id", u.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 Passkey 失败。"})
		return
	}
	slog.Info("passkey registered", "user_id", u.ID, "name", name)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "name": name})
}

// ── Login (discoverable, no username needed) ───────────────────────────

// POST /api/auth/passkey/login/begin
//
// Start a discoverable-credential login. The client calls
// navigator.credentials.get() with no allowList — the browser shows the
// user a list of every passkey their device knows for this domain.
func (s *Server) beginPasskeyLogin(c *gin.Context) {
	wa, err := s.webAuthnInstance()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Passkey 未配置。"})
		return
	}
	options, sessionData, err := wa.BeginDiscoverableLogin()
	if err != nil {
		slog.Error("beginPasskeyLogin", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法开始 Passkey 登录。"})
		return
	}
	if err := s.setSessionValueJSON(c, "webauthn_login", sessionData); err != nil {
		slog.Error("beginPasskeyLogin: store session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法保存 challenge。"})
		return
	}
	c.JSON(http.StatusOK, options)
}

// POST /api/auth/passkey/login/finish { credential }
//
// Complete the login: look up the user by credential id, verify the
// signature, and if it passes, log them in (same Session.Login the
// password path uses).
func (s *Server) finishPasskeyLogin(c *gin.Context) {
	wa, err := s.webAuthnInstance()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Passkey 未配置。"})
		return
	}
	// Read the body once into memory: gin's ShouldBindJSON would consume
	// c.Request.Body, and the go-webauthn library's FinishDiscoverableLogin
	// re-reads that same stream — handing it an already-exhausted body
	// produced an empty parse and every login failed. We parse the wrapper
	// struct ourselves, then feed the captured credential bytes to the
	// library's Body variant, and finally call ValidateDiscoverableLogin
	// (which takes the parsed assertion instead of *http.Request) so there
	// is no second body read at all.
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败。"})
		return
	}
	var in struct {
		Credential json.RawMessage `json:"credential"`
	}
	if err := json.Unmarshal(bodyBytes, &in); err != nil || len(in.Credential) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误。"})
		return
	}
	parsedAssertion, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(in.Credential))
	if err != nil {
		slog.Error("finishPasskeyLogin: parse", "err", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的登录响应。"})
		return
	}
	sessionData, err := s.popSessionValueJSON(c, "webauthn_login")
	if err != nil || sessionData == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "登录会话已过期，请刷新页面后重试。"})
		return
	}
	// Discoverable login: the authenticator only sends the credential id,
	// so the lib calls our resolver to map id → user.
	resolver := func(rawID, userHandle []byte) (webauthn.User, error) {
		// userHandle is what BeginDiscoverableLogin puts in options (we
		// pass empty), so this callback rarely fires. For robustness,
		// prefer the credential id when present.
		if len(rawID) > 0 {
			return passkey.LookupByCredentialID(s.DB, rawID)
		}
		if len(userHandle) > 0 {
			id, err := strconv.Atoi(string(userHandle))
			if err != nil {
				return nil, err
			}
			return passkey.LoadUser(s.DB, id)
		}
		return nil, errors.New("neither credential id nor user handle present")
	}
	// ValidateDiscoverableLogin takes the already-parsed assertion, so the
	// library never touches c.Request (whose Body we consumed above). The
	// resolver maps the credential id → user exactly as before.
	cred, err := wa.ValidateDiscoverableLogin(resolver, *sessionData, parsedAssertion)
	if err != nil {
		slog.Warn("finishPasskeyLogin: verify", "err", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Passkey 验证失败。"})
		return
	}
	// Resolve the user id for the credential that matched, then establish
	// a server session. The lib already ran our resolver and cached the
	// user record, but we need the numeric id to drive the session call.
	owner, err := passkey.LookupByCredentialID(s.DB, cred.ID)
	if err != nil || owner == nil {
		slog.Error("finishPasskeyLogin: owner lookup", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "用户查找失败。"})
		return
	}
	// Persist the updated sign_count (anti-cloning signal).
	if err := passkey.PersistSignCount(s.DB, cred.ID, cred.Authenticator.SignCount); err != nil {
		slog.Warn("finishPasskeyLogin: persist sign_count", "err", err)
	}
	// Establish a server session, same as the password path.
	if err := s.sessions.Login(c.Writer, c.Request, owner.ID, owner.Username); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败。"})
		return
	}
	ip := clientIP(c)
	s.limiter.Reset(owner.Username, ip)
	row, _ := user.GetByID(s.DB, owner.ID)
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "登录成功。",
		"user":    row,
		"next":    "/admin",
	})
}

// ── Listing + revoke ───────────────────────────────────────────────────

// GET /api/auth/passkey — list the current user's registered passkeys.
func (s *Server) listMyPasskeys(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	keys, err := passkey.ListForUser(s.DB, u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载失败。"})
		return
	}
	if keys == nil {
		keys = []passkey.Credential{}
	}
	c.JSON(http.StatusOK, keys)
}

// DELETE /api/auth/passkey/:id — revoke one of the caller's passkeys.
func (s *Server) deleteMyPasskey(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID。"})
		return
	}
	ok, err := passkey.Delete(s.DB, u.ID, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Passkey 不存在。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ── session helpers (JSON-encoded) ─────────────────────────────────────

// setSessionValueJSON marshals v to a string and stashes it under key.
// gorilla/sessions only stores string values.
func (s *Server) setSessionValueJSON(c *gin.Context, key string, v any) error {
	sess, err := s.sessions.Session(c.Request)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sess.Values[key] = string(data)
	return sess.Save(c.Request, c.Writer)
}

// popSessionValueJSON unmarshals and removes key from the session.
func (s *Server) popSessionValueJSON(c *gin.Context, key string) (*webauthn.SessionData, error) {
	sess, err := s.sessions.Session(c.Request)
	if err != nil {
		return nil, err
	}
	raw, ok := sess.Values[key].(string)
	if !ok || raw == "" {
		return nil, nil
	}
	delete(sess.Values, key)
	_ = sess.Save(c.Request, c.Writer)
	var out webauthn.SessionData
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
