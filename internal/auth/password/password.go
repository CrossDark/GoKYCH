package password

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// Hash returns a bcrypt hash of the plaintext password.
func Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verify checks a plaintext password against a stored bcrypt hash.
func Verify(hashed, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
}

// allowedUsernameRe permits any Unicode letter or digit plus the three
// "label-safe" separators (dot, dash, underscore). Everything else —
// whitespace, control chars, punctuation, emoji, path separators, quotes,
// shell metacharacters — is rejected so that the username stays safe to
// embed in cookies, logs, headers and URL fragments without per-call
// escaping, while still letting users pick CJK / Cyrillic / accented Latin
// display names.
var allowedUsernameRe = regexp.MustCompile(`^[\p{L}\p{N}._-]+$`)

// ValidateUsername checks that the username is non-empty, 3–64 runes long,
// and contains only Unicode letters/digits plus . _ -. Whitespace and any
// other "special" characters are rejected. Returns "" if valid.
func ValidateUsername(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "用户名不能为空。"
	}
	runeLen := len([]rune(name))
	if runeLen < 3 || runeLen > 64 {
		return "用户名长度需要在 3-64 个字符之间。"
	}
	if !allowedUsernameRe.MatchString(name) {
		return "用户名只能包含字母、数字以及 . _ -。"
	}
	return ""
}

// ValidateStrength checks password strength rules.
// Returns the first failing error message, or "" if the password is valid.
// The byte-length cap (72) is the real bcrypt limit — bcrypt silently
// truncates beyond 72 bytes, so two differing long passwords would share a
// hash unless we cap up front. Unicode content is welcome: the upper/lower/
// digit checks use unicode.IsUpper / IsLower / IsDigit so a CJK / accented
// character counts toward the category requirements.
//
// Special characters (whitespace + control) are still banned so the password
// can't contain invisible / pasted-from-a-Word-doc content that a user
// couldn't reproduce at a login prompt. All other printable Unicode —
// punctuation, symbols, emoji — is allowed as content.
func ValidateStrength(pw string) string {
	if pw == "" {
		return "密码不能为空。"
	}
	if len(pw) < 8 {
		return "密码长度至少需要 8 个字符。"
	}
	if len(pw) > 72 {
		return "密码长度不能超过 72 个字符。"
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range pw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "密码不能包含空格或控制字符。"
		}
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasUpper {
		return "密码必须包含至少一个大写字母。"
	}
	if !hasLower {
		return "密码必须包含至少一个小写字母。"
	}
	if !hasDigit {
		return "密码必须包含至少一个数字。"
	}
	return ""
}
