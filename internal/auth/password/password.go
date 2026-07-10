package password

import (
	"crypto/rand"
	"math/big"
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

// ValidateUsername checks that the username is non-empty and contains only
// Unicode letters/digits plus . _ -. Whitespace and any other "special"
// characters are rejected. Length is intentionally not constrained here: the
// database column has a practical storage limit, but the app no longer enforces
// a UX-level 3–64 character rule. Returns "" if valid.
func ValidateUsername(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "用户名不能为空。"
	}
	if !allowedUsernameRe.MatchString(name) {
		return "用户名只能包含字母、数字以及 . _ -。"
	}
	return ""
}

type runeRange struct {
	start  rune
	end    rune
	accept func(rune) bool
}

type runeSource struct {
	chars  []rune
	range_ *runeRange
}

const generatedPasswordLength = 18

var generatedPasswordRequiredSources = []runeSource{
	{chars: []rune("0123456789")},
	{chars: []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")},
	{chars: []rune("abcdefghijklmnopqrstuvwxyz")},
	{range_: &runeRange{start: 0x00C0, end: 0x024F, accept: unicode.IsLetter}}, // Latin-1 + Latin Extended
	{range_: &runeRange{start: 0x4E00, end: 0x9FFF}},                           // CJK Unified Ideographs
	{range_: &runeRange{start: 0x3041, end: 0x30FA, accept: unicode.IsLetter}}, // Hiragana + Katakana
}

var generatedPasswordAllSources = []runeSource{
	{chars: []rune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")},
	{range_: &runeRange{start: 0x00C0, end: 0x024F, accept: unicode.IsLetter}}, // Latin letters beyond plain English
	{range_: &runeRange{start: 0x0370, end: 0x03FF, accept: unicode.IsLetter}}, // Greek
	{range_: &runeRange{start: 0x0400, end: 0x04FF, accept: unicode.IsLetter}}, // Cyrillic
	{range_: &runeRange{start: 0x3041, end: 0x30FA, accept: unicode.IsLetter}}, // Japanese kana
	{range_: &runeRange{start: 0x4E00, end: 0x9FFF}},                           // CJK
	{range_: &runeRange{start: 0xAC00, end: 0xD7A3}},                           // Hangul syllables
	{chars: []rune("!#$%&*+-=?@^_~·•§№℃☆★◇◆○●◎♠♣♥♦♪♫✓∞∑πΩ")},
}

// GenerateRandom returns a system-generated password. It deliberately mixes
// digits, ASCII English letters, extended Latin letters, CJK characters and
// Japanese kana, then fills the rest from additional printable Unicode pools.
// The length is capped so the UTF-8 byte length stays well under bcrypt's
// 72-byte limit while still being too random for a human-chosen password policy
// to matter. The plaintext is meant to be shown once to the operator/user.
func GenerateRandom() (string, error) {
	out := make([]rune, 0, generatedPasswordLength)
	for _, src := range generatedPasswordRequiredSources {
		r, err := src.pick()
		if err != nil {
			return "", err
		}
		out = append(out, r)
	}
	for len(out) < generatedPasswordLength {
		idx, err := randomIndex(len(generatedPasswordAllSources))
		if err != nil {
			return "", err
		}
		r, err := generatedPasswordAllSources[idx].pick()
		if err != nil {
			return "", err
		}
		out = append(out, r)
	}
	if err := shuffleRunes(out); err != nil {
		return "", err
	}
	return string(out), nil
}

func (s runeSource) pick() (rune, error) {
	if len(s.chars) > 0 {
		idx, err := randomIndex(len(s.chars))
		if err != nil {
			return 0, err
		}
		return s.chars[idx], nil
	}
	if s.range_ == nil {
		return 0, nil
	}
	return randomRuneFromRange(*s.range_)
}

func randomRuneFromRange(rr runeRange) (rune, error) {
	span := int(rr.end - rr.start + 1)
	for {
		idx, err := randomIndex(span)
		if err != nil {
			return 0, err
		}
		r := rr.start + rune(idx)
		if rr.accept == nil || rr.accept(r) {
			return r, nil
		}
	}
}

func randomIndex(n int) (int, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

func shuffleRunes(rs []rune) error {
	for i := len(rs) - 1; i > 0; i-- {
		j, err := randomIndex(i + 1)
		if err != nil {
			return err
		}
		rs[i], rs[j] = rs[j], rs[i]
	}
	return nil
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
