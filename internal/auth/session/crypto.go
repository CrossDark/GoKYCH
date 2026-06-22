package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// generateToken returns a 64-char hex string (32 random bytes).
// Returns an error if the system CSPRNG fails, so callers never silently use
// a short/zero buffer (which would weaken CSRF/session token security).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// constantTimeCompare returns true if a == b using constant-time comparison.
func constantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
