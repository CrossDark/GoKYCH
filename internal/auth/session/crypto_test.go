package session

import (
	"encoding/hex"
	"testing"
)

func TestGenerateTokenIs64CharHex(t *testing.T) {
	tok, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if len(tok) != 64 {
		t.Fatalf("expected 64-char hex (32 random bytes), got %d chars: %q", len(tok), tok)
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Fatalf("output must be valid hex, got %v", err)
	}
}

func TestGenerateTokenIsUnique(t *testing.T) {
	// Statistical sanity: 100 tokens should produce no collisions. crypto/rand
	// collisions at 32 bytes are astronomically unlikely; a duplicate here
	// means the RNG is broken.
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if seen[tok] {
			t.Fatalf("token collision at iteration %d: %q", i, tok)
		}
		seen[tok] = true
	}
}

func TestConstantTimeCompareEqual(t *testing.T) {
	if !constantTimeCompare("abc123", "abc123") {
		t.Fatalf("equal strings must compare true")
	}
}

func TestConstantTimeCompareDifferent(t *testing.T) {
	if constantTimeCompare("abc123", "abc124") {
		t.Fatalf("different strings must compare false")
	}
}

func TestConstantTimeCompareDifferentLength(t *testing.T) {
	// constantTimeCompare goes through subtle.ConstantTimeCompare, which
	// returns 0 for different-length inputs — the calling code checks `== 1`
	// so the result must be false.
	if constantTimeCompare("short", "longer-string") {
		t.Fatalf("different-length strings must compare false")
	}
}

func TestConstantTimeCompareEmpty(t *testing.T) {
	if !constantTimeCompare("", "") {
		t.Fatalf("two empty strings should compare equal")
	}
	if constantTimeCompare("", "x") {
		t.Fatalf("empty vs non-empty must compare false")
	}
}