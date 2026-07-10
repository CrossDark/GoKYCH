package api

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/passkey"
	"gokych/internal/auth/password"
	"gokych/internal/auth/user"
	"gokych/internal/core/settings"
)

// ─── Captcha (math problem, answer stored in session, single-use) ─────────

type captchaQA struct {
	// Mode is the active captcha mode for this question ("math" or "matrix").
	// Kept here so the frontend can render a matching input hint (e.g. "请输入
	// 矩阵答案 [[a,b],[c,d]]") without a second round-trip to /api/admin/settings.
	Mode     string `json:"mode"`
	Question string `json:"question"`
}

// captchaEnvelope is what we store in the session under the "captcha" key.
// Carrying mode + canonical answer together lets verifyCaptcha dispatch
// without reloading settings (which is expensive and could race against
// the owner toggling the mode mid-login).
type captchaEnvelope struct {
	Mode   string `json:"mode"`
	Answer string `json:"answer"`
}

// cryptoIntn returns a cryptographically secure random integer in [0, n).
// Uses crypto/rand so captcha operands aren't predictable from the process
// state (math/rand is seeded from time and was trivially forecastable).
func cryptoIntn(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

// captchaMode reads settings.features.captcha_mode and clamps it to a known
// value. Anything other than "matrix" (incl. unset / corrupt) falls back to
// "math" so a misconfigured settings.yml can't lock the owner out.
func captchaMode(cfg map[string]interface{}) string {
	if cfg == nil {
		return "math"
	}
	features, _ := cfg["features"].(map[string]interface{})
	if features == nil {
		return "math"
	}
	v, _ := features["captcha_mode"].(string)
	if v == "matrix" {
		return "matrix"
	}
	return "math"
}

// generateMathCaptcha builds a decimal arithmetic question with operands in
// [1, 20] and operators chosen uniformly from {+, -, ×}. The user enters the
// answer as a decimal integer. The canonical (decimal-string) answer and the
// mode are stored together via storeCaptcha — verifyCaptcha dispatches on mode.
func (s *Server) generateMathCaptcha(c *gin.Context, qa *captchaQA) {
	a, _ := cryptoIntn(20)
	a++ // 1..20
	b, _ := cryptoIntn(20)
	b++
	var op string
	var answer int
	opChoice, _ := cryptoIntn(3)
	switch opChoice {
	case 0:
		op = "+"
		answer = a + b
	case 1:
		// guarantee non-negative
		if a < b {
			a, b = b, a
		}
		op = "-"
		answer = a - b
	default:
		op = "×"
		answer = a * b
	}
	qa.Question = strconv.Itoa(a) + " " + op + " " + strconv.Itoa(b) + " = ?"
	s.storeCaptcha(c, "math", strconv.Itoa(answer))
}

// matrix2 is a 2×2 row-major integer matrix. Displayed and persisted as
// compact JSON "[[a,b],[c,d]]" so the user input shape matches the answer
// shape and we can verify with a JSON parse on both sides.
type matrix2 [2][2]int

// String renders the canonical compact form. Use this for both the displayed
// question and the stored answer so they're byte-equal on success.
func (m matrix2) String() string {
	return fmt.Sprintf("[[%d,%d],[%d,%d]]", m[0][0], m[0][1], m[1][0], m[1][1])
}

// Mul is standard 2×2 matrix multiplication. With operands in [-9, 9] the
// largest cell is 9*9 + 9*9 = 162, well within int and trivial for humans
// with pencil-and-paper (or a calculator).
func (m matrix2) Mul(o matrix2) matrix2 {
	return matrix2{
		{
			m[0][0]*o[0][0] + m[0][1]*o[1][0],
			m[0][0]*o[0][1] + m[0][1]*o[1][1],
		},
		{
			m[1][0]*o[0][0] + m[1][1]*o[1][0],
			m[1][0]*o[0][1] + m[1][1]*o[1][1],
		},
	}
}

// randMatrix2 returns a 2×2 matrix with each entry drawn uniformly from
// [-9, 9]. Single-digit range keeps the multiplication mentally tractable for
// legitimate humans while remaining a meaningful step up from "math" mode.
func randMatrix2() matrix2 {
	var m matrix2
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			v, _ := cryptoIntn(19) // 0..18
			m[i][j] = v - 9        // -9..9
		}
	}
	return m
}

// generateMatrixCaptcha renders a 2×2 integer multiplication problem and
// stores the canonical answer matrix as a compact JSON string in the captcha
// envelope. The user must reply in the same JSON shape, e.g. "[[6,8],[10,12]]".
// Spaces inside the brackets are accepted by verifyMatrixAnswer's JSON parser.
func (s *Server) generateMatrixCaptcha(c *gin.Context, qa *captchaQA) {
	a := randMatrix2()
	b := randMatrix2()
	s.storeCaptcha(c, "matrix", a.Mul(b).String())
	qa.Question = a.String() + " × " + b.String() + " = ?"
}

// generateCaptcha creates a captcha problem whose mode is decided by the
// site's `features.captcha_mode` setting. The answer (decimal string for
// "math", JSON matrix string for "matrix") is stored in the session under
// `captcha` together with its mode — verifyCaptcha pops it on first attempt
// and dispatches on the embedded mode.
func (s *Server) generateCaptcha(c *gin.Context) captchaQA {
	mode := captchaMode(nil) // safe fallback before settings load
	cfg, err := settings.Load(s.DataDir)
	if err == nil {
		mode = captchaMode(cfg)
	}
	qa := captchaQA{Mode: mode}
	switch mode {
	case "matrix":
		s.generateMatrixCaptcha(c, &qa)
	default:
		s.generateMathCaptcha(c, &qa)
	}
	return qa
}

// verifyMatrixAnswer deep-compares the user-submitted JSON matrix against the
// stored one. Both sides must parse as a 2×2 int matrix; any shape mismatch
// (different outer length, wrong row length, non-int element) returns false.
// We deliberately don't differentiate "syntax error" from "wrong number" so
// the captcha can't leak which kind of mistake the user made.
func verifyMatrixAnswer(stored, input string) bool {
	var a, b [][]int
	if err := json.Unmarshal([]byte(stored), &a); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &b); err != nil {
		return false
	}
	if len(a) != 2 || len(b) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		if len(a[i]) != 2 || len(b[i]) != 2 {
			return false
		}
		for j := 0; j < 2; j++ {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// verifyCaptcha pops the captcha envelope (single-use) and dispatches by mode.
// Math mode keeps the original byte-exact constant-time compare. Matrix mode
// parses both sides as JSON and compares element-wise — element count is fixed
// at 4×int64 so the comparison itself is effectively constant-time too.
func (s *Server) verifyCaptcha(c *gin.Context, input string) bool {
	if input == "" {
		return false
	}
	raw, ok := s.popSessionValue(c, "captcha")
	if !ok {
		return false
	}
	var env captchaEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return false
	}
	trimmed := strings.TrimSpace(input)
	switch env.Mode {
	case "matrix":
		return verifyMatrixAnswer(env.Answer, trimmed)
	default:
		return subtle.ConstantTimeCompare([]byte(trimmed), []byte(env.Answer)) == 1
	}
}

// storeCaptcha marshals {mode, answer} to JSON and writes it under the
// "captcha" session key, replacing any prior envelope (single-use semantics).
func (s *Server) storeCaptcha(c *gin.Context, mode, answer string) {
	raw, err := json.Marshal(captchaEnvelope{Mode: mode, Answer: answer})
	if err != nil {
		return
	}
	s.setSessionValue(c, "captcha", string(raw))
}

// ─── Handlers ────────────────────────────────────────────────────────────

// GET /api/auth/me — returns the current user or null.
func (s *Server) getMe(c *gin.Context) {
	u := CurrentUserFromContext(c)
	if u == nil {
		c.JSON(http.StatusOK, gin.H{"user": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}

// GET /api/auth/csrf — returns (or creates) the CSRF token and a fresh captcha.
// Frontend calls this before rendering the login form.
func (s *Server) getCSRF(c *gin.Context) {
	tok, err := s.sessions.CSRFToken(c.Writer, c.Request)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "会话错误。"})
		return
	}
	captcha := s.generateCaptcha(c)
	c.JSON(http.StatusOK, gin.H{
		"csrf_token": tok,
		"captcha":    captcha,
	})
}

// LoginRequest is the JSON body for POST /api/auth/login.
type LoginRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	Captcha   string `json:"captcha"`
	CSRFToken string `json:"csrf_token"`
	Next      string `json:"next"`
}

// loginError re-generates captcha + CSRF and returns a 400 with the error.
func (s *Server) loginError(c *gin.Context, code int, msg string) {
	tok, _ := s.sessions.CSRFToken(c.Writer, c.Request)
	captcha := s.generateCaptcha(c)
	c.JSON(code, gin.H{
		"error":      msg,
		"csrf_token": tok,
		"captcha":    captcha,
	})
}

// POST /api/auth/login — authenticates and establishes a session.
func (s *Server) postLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.loginError(c, http.StatusBadRequest, "请求格式错误。")
		return
	}

	username := user.NormalizeUsername(req.Username)
	ip := clientIP(c)

	// 1. CSRF check.
	if !s.sessions.VerifyCSRF(c.Request, req.CSRFToken) {
		s.loginError(c, http.StatusBadRequest, "安全验证失败，请刷新页面后重试。")
		return
	}

	// 2. Empty fields.
	if username == "" || req.Password == "" {
		s.loginError(c, http.StatusBadRequest, "用户名和密码不能为空。")
		return
	}

	// 3. Captcha (single-use).
	if !s.verifyCaptcha(c, req.Captcha) {
		s.loginError(c, http.StatusBadRequest, "验证码错误，请重试。")
		return
	}

	// 4. Rate limit.
	if allowed, msg := s.limiter.CheckAllowed(username, ip); !allowed {
		s.loginError(c, http.StatusTooManyRequests, msg)
		return
	}

	// 5. Credential lookup.
	ctx := c.Request.Context()
	uwp, err := user.GetWithPasswordCtx(ctx, s.DB, username)
	if err != nil && err != sql.ErrNoRows {
		s.loginError(c, http.StatusInternalServerError, "服务器错误，请稍后重试。")
		return
	}
	if uwp == nil || !password.Verify(uwp.PasswordHash, req.Password) {
		s.limiter.RecordFailure(username, ip)
		s.loginError(c, http.StatusUnauthorized, "用户名或密码错误。")
		return
	}

	// 5b. If this user has a passkey registered, the password path is
	// disabled — passkey is the only way in. Owner is exempt so the
	// bootstrap admin can never lock themselves out of the system.
	if !user.IsOwner(uwp.Role) {
		has, err := passkey.HasAnyCtx(ctx, s.DB, uwp.ID)
		if err != nil {
			slog.Error("postLogin: passkey check", "user_id", uwp.ID, "err", err)
		} else if has {
			s.limiter.RecordFailure(username, ip)
			s.loginError(c, http.StatusForbidden, "该账号已启用 Passkey，请使用 Passkey 登录。")
			return
		}
	}

	// 6. Success.
	if err := s.sessions.Login(c.Writer, c.Request, uwp.ID, uwp.Username); err != nil {
		s.loginError(c, http.StatusInternalServerError, "登录失败，请重试。")
		return
	}
	s.limiter.Reset(username, ip)

	// Safe redirect target (open-redirect guard). Default landing page
	// depends on role: regular users have nothing to do in the admin
	// dashboard, so send them straight to their profile (where password
	// + passkey management live); admins/owners keep going to /admin.
	if req.Next == "" && !user.IsAdmin(uwp.Role) {
		req.Next = "/admin/profile"
	}
	next := sanitizeNext(req.Next)

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"user":    &uwp.User,
		"next":    next,
		"message": "登录成功。",
	})
}

// POST /api/auth/logout — clears the session.
func (s *Server) postLogout(c *gin.Context) {
	_ = s.sessions.Logout(c.Writer, c.Request)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "已退出登录。"})
}

// ─── helpers ────────────────────────────────────────────────────────────

// sanitizeNext guards against open redirects: only same-origin relative paths
// are allowed. It rejects anything with a scheme/host/userinfo (e.g.
// //evil.com, http://evil.com, user@host) and backslash/control-char tricks
// (/\evil.com, %2F%2fevil.com) that some browsers interpret as absolute.
// Falls back to "/admin" on any rejection.
func sanitizeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/admin"
	}
	u, err := url.Parse(next)
	if err != nil {
		return "/admin"
	}
	if u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return "/admin"
	}
	// Reject backslashes: url.Parse leaves them in Path, but browsers may
	// treat /\evil.com as protocol-relative.
	if strings.Contains(u.Path, "\\") {
		return "/admin"
	}
	return u.Path
}

// setSessionValue stores a value in the session and persists it.
func (s *Server) setSessionValue(c *gin.Context, key string, val interface{}) {
	sess, err := s.sessions.Session(c.Request)
	if err != nil {
		return
	}
	sess.Values[key] = val
	_ = sess.Save(c.Request, c.Writer)
}

// popSessionValue removes and returns a stringified session value, saving the session.
func (s *Server) popSessionValue(c *gin.Context, key string) (string, bool) {
	sess, err := s.sessions.Session(c.Request)
	if err != nil {
		return "", false
	}
	v, ok := sess.Values[key]
	if !ok {
		return "", false
	}
	delete(sess.Values, key)
	_ = sess.Save(c.Request, c.Writer)
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.FormatInt(int64(t), 10), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatInt(int64(t), 10), true
	default:
		return "", false
	}
}
