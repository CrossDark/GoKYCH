package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// generateToken returns a 64-char hex string (32 random bytes).
func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// constantTimeCompare returns true if a == b using constant-time comparison.
func constantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
