package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"gokych/internal/auth/passkey"
	"gokych/internal/auth/user"
)

const maxCredentialNameLen = 64

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
	ctx := c.Request.Context()
	pkUser, err := passkey.LoadUserCtx(ctx, s.DB, u.ID)
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
	if err := s.setSessionValueJSON(c, "webauthn_reg", sessionData); err != nil {
		slog.Error("beginPasskeyRegistration: store session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法保存 challenge。"})
		return
	}
	c.JSON(http.StatusOK, options)
}

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
	ctx := c.Request.Context()
	pkUser, err := passkey.LoadUserCtx(ctx, s.DB, u.ID)
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
	if err := passkey.SaveCredentialCtx(ctx, s.DB, u.ID, name, cred); err != nil {
		slog.Error("finishPasskeyRegistration: save", "user_id", u.ID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 Passkey 失败。"})
		return
	}
	slog.Info("passkey registered", "user_id", u.ID, "name", name)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "name": name})
}

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

func (s *Server) finishPasskeyLogin(c *gin.Context) {
	wa, err := s.webAuthnInstance()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Passkey 未配置。"})
		return
	}
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
	ctx := c.Request.Context()
	resolver := func(rawID, userHandle []byte) (webauthn.User, error) {
		if len(rawID) > 0 {
			return passkey.LookupByCredentialIDCtx(ctx, s.DB, rawID)
		}
		if len(userHandle) > 0 {
			id, err := strconv.Atoi(string(userHandle))
			if err != nil {
				return nil, err
			}
			return passkey.LoadUserCtx(ctx, s.DB, id)
		}
		return nil, errors.New("neither credential id nor user handle present")
	}
	cred, err := wa.ValidateDiscoverableLogin(resolver, *sessionData, parsedAssertion)
	if err != nil {
		if errors.Is(err, passkey.ErrCredentialNotFound) {
			slog.Warn("finishPasskeyLogin: credential unknown to server", "err", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "此 Passkey 已在本站撤销。请用密码登录后重新登记，或换一个 Passkey。"})
			return
		}
		slog.Warn("finishPasskeyLogin: verify", "err", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Passkey 验证失败。"})
		return
	}
	owner, err := passkey.LookupByCredentialIDCtx(ctx, s.DB, cred.ID)
	if err != nil || owner == nil {
		slog.Error("finishPasskeyLogin: owner lookup", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "用户查找失败。"})
		return
	}
	if err := passkey.PersistSignCountCtx(ctx, s.DB, cred.ID, cred.Authenticator.SignCount); err != nil {
		slog.Warn("finishPasskeyLogin: persist sign_count", "err", err)
	}
	if err := s.sessions.Login(c.Writer, c.Request, owner.ID, owner.Username); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败。"})
		return
	}
	ip := clientIP(c)
	s.limiter.Reset(owner.Username, ip)
	row, _ := user.GetByIDCtx(ctx, s.DB, owner.ID)
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "登录成功。",
		"user":    row,
		"next":    "/admin",
	})
}

func (s *Server) listMyPasskeys(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	ctx := c.Request.Context()
	keys, err := passkey.ListForUserCtx(ctx, s.DB, u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载失败。"})
		return
	}
	if keys == nil {
		keys = []passkey.Credential{}
	}
	c.JSON(http.StatusOK, keys)
}

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
	ctx := c.Request.Context()
	ok, err := passkey.DeleteCtx(ctx, s.DB, u.ID, id)
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

func (s *Server) listAllPasskeys(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.DB.QueryContext(ctx,
		`SELECT wc.id, wc.user_id, u.username, u.nickname, wc.name,
		        wc.credential_id, wc.transports, wc.sign_count, wc.created_at
		 FROM webauthn_credentials wc
		 JOIN users u ON u.id = wc.user_id
		 ORDER BY u.username, wc.created_at DESC`)
	if err != nil {
		slog.Error("listAllPasskeys", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载 Passkey 列表失败。"})
		return
	}
	defer rows.Close()
	type rowPasskey struct {
		ID           int64    `json:"id"`
		UserID       int      `json:"user_id"`
		UserName     string   `json:"user_name"`
		UserNickname string   `json:"user_nickname"`
		Name         string   `json:"name"`
		CredentialID string   `json:"credential_id"`
		Transports   []string `json:"transports"`
		SignCount    uint32   `json:"sign_count"`
		CreatedAt    string   `json:"created_at"`
	}
	out := make([]rowPasskey, 0)
	for rows.Next() {
		var r rowPasskey
		var transports, nickname sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserName, &nickname, &r.Name,
			&r.CredentialID, &transports, &r.SignCount, &r.CreatedAt); err != nil {
			continue
		}
		r.UserNickname = nickname.String
		if transports.Valid && transports.String != "" {
			r.Transports = strings.Split(transports.String, ",")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		slog.Error("listAllPasskeys: iterate rows", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加载 Passkey 列表失败。"})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) deleteAnyPasskey(c *gin.Context) {
	caller := CurrentUserFromContext(c)
	if caller == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录。"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID。"})
		return
	}
	ctx := c.Request.Context()
	var ownerID int
	var ownerName string
	err = s.DB.QueryRowContext(ctx,
		`SELECT wc.user_id, u.username FROM webauthn_credentials wc
		 JOIN users u ON u.id = wc.user_id WHERE wc.id = ?`, id).Scan(&ownerID, &ownerName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Passkey 不存在。"})
		return
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ?`, id)
	if err != nil {
		slog.Error("deleteAnyPasskey", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败。"})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Passkey 不存在。"})
		return
	}
	slog.Info("owner deleted passkey", "caller", caller.ID, "target_user", ownerID, "target_username", ownerName, "passkey_id", id)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

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
