package api

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gokych/internal/auth/password"
	"gokych/internal/auth/user"
)

// ─── Captcha (math problem, answer stored in session, single-use) ─────────

type captchaQA struct {
	Question string `json:"question"`
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

// generateCaptcha creates a math captcha, stores the answer in the session,
// and returns the question. The answer is consumed on verification.
func (s *Server) generateCaptcha(c *gin.Context) captchaQA {
	a, err1 := cryptoIntn(20)
	if err1 != nil {
		a = 0
	}
	a++ // 1..20
	b, err2 := cryptoIntn(20)
	if err2 != nil {
		b = 0
	}
	b++
	var op string
	var answer int
	opChoice, err3 := cryptoIntn(3)
	if err3 != nil {
		opChoice = 0
	}
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
	question := strconv.Itoa(a) + " " + op + " " + strconv.Itoa(b) + " = ?"
	s.setSessionValue(c, "captcha_answer", answer)
	return captchaQA{Question: question}
}

// verifyCaptcha checks the user's answer against the stored one (single-use,
// constant-time). Returns false if no captcha is pending.
func (s *Server) verifyCaptcha(c *gin.Context, input string) bool {
	if input == "" {
		return false
	}
	stored, ok := s.popSessionValue(c, "captcha_answer")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(input)), []byte(stored)) == 1
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
	uwp, err := user.GetWithPassword(s.DB, username)
	if err != nil && err != sql.ErrNoRows {
		s.loginError(c, http.StatusInternalServerError, "服务器错误，请稍后重试。")
		return
	}
	if uwp == nil || !password.Verify(uwp.PasswordHash, req.Password) {
		s.limiter.RecordFailure(username, ip)
		s.loginError(c, http.StatusUnauthorized, "用户名或密码错误。")
		return
	}

	// 6. Success.
	if err := s.sessions.Login(c.Writer, c.Request, uwp.ID, uwp.Username); err != nil {
		s.loginError(c, http.StatusInternalServerError, "登录失败，请重试。")
		return
	}
	s.limiter.Reset(username, ip)

	// Safe redirect target (open-redirect guard).
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
