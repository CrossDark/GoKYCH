package password

import (
	"strings"
	"testing"
)

// TestValidateStrength walks every documented rule to make sure a future
// refactor doesn't accidentally weaken the policy (e.g. by lowering the
// minimum length or dropping the digit requirement).
func TestValidateStrength(t *testing.T) {
	cases := []struct {
		name      string
		pw        string
		wantOK    bool
		wantInMsg string // optional substring expected in the error message
	}{
		{name: "empty rejected", pw: "", wantOK: false, wantInMsg: "不能为空"},
		{name: "too short rejected", pw: "Aa1", wantOK: false, wantInMsg: "8"},
		{name: "too long rejected (bcrypt 72-byte cap)", pw: strings.Repeat("A", 73) + "a1", wantOK: false, wantInMsg: "72"},
		{name: "whitespace rejected", pw: "Good Pass1", wantOK: false, wantInMsg: "空格"},
		{name: "control char rejected", pw: "Good\tPass1", wantOK: false, wantInMsg: "控制"},
		{name: "no uppercase rejected", pw: "all-lower1!", wantOK: false, wantInMsg: "大写"},
		{name: "no lowercase rejected", pw: "ALL-UPPER1!", wantOK: false, wantInMsg: "小写"},
		{name: "no digit rejected", pw: "NoDigitsHere!", wantOK: false, wantInMsg: "数字"},
		{name: "ok", pw: "GoodPass1", wantOK: true},
		{name: "unicode ok", pw: "你好Password1", wantOK: true},
		{name: "unicode-only categories satisfy checks", pw: "密码Ｐa1", wantOK: true},
		{name: "exactly 8 ok", pw: "Aa1aaaa!", wantOK: true},
		{name: "exactly 72 ok", pw: "Aa1" + strings.Repeat("x", 69), wantOK: true},
		{name: "emoji content ok", pw: "🚀Aa1234567890", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateStrength(tc.pw)
			if tc.wantOK {
				if got != "" {
					t.Fatalf("expected OK, got error %q", got)
				}
				return
			}
			if got == "" {
				t.Fatalf("expected error, got OK")
			}
			if tc.wantInMsg != "" && !strings.Contains(got, tc.wantInMsg) {
				t.Fatalf("error %q does not contain %q", got, tc.wantInMsg)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantOK    bool
		wantInMsg string
	}{
		{name: "empty rejected", input: "", wantOK: false, wantInMsg: "不能为空"},
		{name: "trimmed empty rejected", input: "   ", wantOK: false, wantInMsg: "不能为空"},
		{name: "single char ok", input: "李", wantOK: true},
		{name: "very long ok", input: strings.Repeat("字", 160), wantOK: true},
		{name: "whitespace inside rejected", input: "ab cd", wantOK: false, wantInMsg: "字母"},
		{name: "special char rejected", input: "user@test", wantOK: false, wantInMsg: "字母"},
		{name: "path separator rejected", input: "a/b", wantOK: false, wantInMsg: "字母"},
		{name: "emoji rejected", input: "user😀", wantOK: false, wantInMsg: "字母"},
		{name: "ok lower ascii", input: "alice", wantOK: true},
		{name: "ok mixed case + digits + dash", input: "Bob-2024", wantOK: true},
		{name: "ok unicode CJK", input: "用户一号", wantOK: true},
		{name: "ok unicode + digits", input: "用户1", wantOK: true},
		{name: "ok Cyrillic", input: "Иван_2024", wantOK: true},
		{name: "ok accented Latin", input: "Jérôme.OB", wantOK: true},
		{name: "ok formerly too short", input: "ab", wantOK: true},
		{name: "ok formerly too long", input: strings.Repeat("字", 80), wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateUsername(tc.input)
			if tc.wantOK {
				if got != "" {
					t.Fatalf("expected OK, got error %q", got)
				}
				return
			}
			if got == "" {
				t.Fatalf("expected error, got OK")
			}
			if tc.wantInMsg != "" && !strings.Contains(got, tc.wantInMsg) {
				t.Fatalf("error %q does not contain %q", got, tc.wantInMsg)
			}
		})
	}
}

func TestGenerateRandom(t *testing.T) {
	for i := 0; i < 20; i++ {
		plain, err := GenerateRandom()
		if err != nil {
			t.Fatalf("GenerateRandom: %v", err)
		}
		if len([]rune(plain)) != generatedPasswordLength {
			t.Fatalf("generated password length = %d runes, want %d: %q", len([]rune(plain)), generatedPasswordLength, plain)
		}
		if len([]byte(plain)) > 72 {
			t.Fatalf("generated password is too long for bcrypt: %d bytes", len([]byte(plain)))
		}
		if ValidateStrength(plain) != "" {
			t.Fatalf("generated password should satisfy legacy strength validator: %q", plain)
		}
		if !strings.ContainsFunc(plain, func(r rune) bool { return r > 127 }) {
			t.Fatalf("generated password should include non-ASCII Unicode: %q", plain)
		}
		hash, err := Hash(plain)
		if err != nil {
			t.Fatalf("Hash generated password: %v", err)
		}
		if !Verify(hash, plain) {
			t.Fatalf("Verify should accept generated password")
		}
	}
}

func TestHashAndVerify(t *testing.T) {
	const plain = "GoodPass1"
	hash, err := Hash(plain)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if hash == plain {
		t.Fatalf("hash should not equal plaintext")
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash prefix $2..., got %q", hash[:min(4, len(hash))])
	}
	if !Verify(hash, plain) {
		t.Fatalf("Verify should accept the same plaintext")
	}
	if Verify(hash, "wrongpassword") {
		t.Fatalf("Verify should reject a wrong password")
	}
}

func TestVerifyConstantTimeForWrongPrefix(t *testing.T) {
	// Sanity: bcrypt hashes start with $2a$ / $2b$ / $2y$. A string with the
	// wrong prefix must NOT be accepted as a valid hash — even though
	// bcrypt.CompareHashAndPassword itself returns an error, we want to be
	// sure we don't accidentally short-circuit to true on bad input.
	if Verify("not-a-bcrypt-hash", "anything") {
		t.Fatalf("Verify must reject malformed hashes")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
