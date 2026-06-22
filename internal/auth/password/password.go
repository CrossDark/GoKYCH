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

var (
	reWhitespace = regexp.MustCompile(`\s`)
	reUpper      = regexp.MustCompile(`[A-Z]`)
	reLower      = regexp.MustCompile(`[a-z]`)
	reDigit      = regexp.MustCompile(`[0-9]`)
)

// ValidateStrength checks password strength rules.
// Returns the first failing error message, or "" if the password is valid.
// Rules (checked in order, matching PyKYCH):
//  1. non-empty
//  2. 8-128 chars
//  3. no whitespace
//  4. ≥1 uppercase
//  5. ≥1 lowercase
//  6. ≥1 digit
func ValidateStrength(pw string) string {
	if pw == "" {
		return "密码不能为空。"
	}
	if len(pw) < 8 {
		return "密码长度至少需要 8 个字符。"
	}
	if len(pw) > 128 {
		return "密码长度不能超过 128 个字符。"
	}
	if reWhitespace.MatchString(pw) {
		return "密码不能包含空格或空白字符。"
	}
	if !reUpper.MatchString(pw) {
		return "密码必须包含至少一个大写字母 (A-Z)。"
	}
	if !reLower.MatchString(pw) {
		return "密码必须包含至少一个小写字母 (a-z)。"
	}
	if !reDigit.MatchString(pw) {
		return "密码必须包含至少一个数字 (0-9)。"
	}
	return ""
}

// ValidateUsername checks username length is within 3-64 chars and contains
// no whitespace. Returns "" if valid.
func ValidateUsername(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "用户名不能为空。"
	}
	if len(name) < 3 || len(name) > 64 {
		return "用户名长度需要在 3-64 个字符之间。"
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return "用户名不能包含空格或空白字符。"
		}
	}
	return ""
}
