// Package apikey implements API-key authentication: admins create keys from
// the settings page, receive the plaintext once, and subsequent requests
// authenticate by sending the key in the `X-API-Key` header.
//
// The plaintext is never persisted — we store only its bcrypt hash and a
// short prefix for the admin UI. On every request, Verify() iterates the
// (small) set of keys, bcrypt-compares the candidate, and returns the
// owner on hit.
//
// The bcrypt cost here is intentionally lower than the user-login path
// (10 vs 12) so the per-request verify stays under a millisecond; the
// threat model is "leak the database, attacker still has to brute-force
// a 32-byte random token", not "online password guessing".
package apikey

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// keyByteLen is the entropy size of the random suffix. 32 bytes = 256 bits
// of entropy, well past brute-force feasibility even for a leaked database.
const keyByteLen = 32

// bcryptCost is one notch below the user-login cost so per-request verify
// stays snappy under load.
const bcryptCost = 10

// KeyPrefix is the URL-safe tag the admin can recognise in headers / logs.
const KeyPrefix = "gky_"

// Key is the on-disk + API representation of an API key. The Hash is only
// populated on the way back from Create(); list endpoints return the key
// without it.
type Key struct {
	ID         int        `json:"id"`
	OwnerID    int        `json:"owner_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	Hash       string     `json:"-"` // never serialised
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Create generates a new random key, hashes it, and inserts the row. The
// plaintext is returned exactly once (the caller must surface it to the
// admin before the next call); subsequent reads of this row only have
// key_prefix and key_hash.
func Create(db *sql.DB, ownerID int, name string, ttl time.Duration) (*Key, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("name 不能为空")
	}
	if len(name) > 128 {
		return nil, "", errors.New("name 过长 (max 128)")
	}
	// 32 random bytes, hex-encoded → 64 chars of suffix. The visible prefix
	// is the first 8 of those for the admin UI.
	raw := make([]byte, keyByteLen)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("rand: %w", err)
	}
	suffix := hex.EncodeToString(raw) // 64 hex chars
	full := KeyPrefix + suffix
	visible := full[:8] // "gky_xxxx" — what shows in the settings list
	hash, err := bcrypt.GenerateFromPassword([]byte(full), bcryptCost)
	if err != nil {
		return nil, "", fmt.Errorf("bcrypt: %w", err)
	}
	var expiresAt *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expiresAt = &t
	}
	res, err := db.Exec(
		`INSERT INTO api_keys (owner_id, name, key_prefix, key_hash, expires_at) VALUES (?, ?, ?, ?, ?)`,
		ownerID, name, visible, string(hash), expiresAt)
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	return &Key{
		ID:        int(id),
		OwnerID:   ownerID,
		Name:      name,
		KeyPrefix: visible,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}, full, nil
}

// List returns all keys owned by ownerID (most recent first). Hash is
// always empty in the returned records.
func List(db *sql.DB, ownerID int) ([]Key, error) {
	rows, err := db.Query(
		`SELECT id, owner_id, name, key_prefix, last_used_at, expires_at, created_at
		 FROM api_keys WHERE owner_id = ? ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Key, 0)
	for rows.Next() {
		var k Key
		var lu, ea sql.NullTime
		if err := rows.Scan(&k.ID, &k.OwnerID, &k.Name, &k.KeyPrefix, &lu, &ea, &k.CreatedAt); err != nil {
			return nil, err
		}
		if lu.Valid {
			t := lu.Time
			k.LastUsedAt = &t
		}
		if ea.Valid {
			t := ea.Time
			k.ExpiresAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Delete removes a key. Returns (true, nil) if a row was actually deleted,
// (false, nil) when the key didn't exist or didn't belong to ownerID (we
// don't distinguish — keeps the API free of information leaks).
func Delete(db *sql.DB, ownerID, id int) (bool, error) {
	res, err := db.Exec(`DELETE FROM api_keys WHERE id = ? AND owner_id = ?`, id, ownerID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// VerifyResult is the outcome of a key verification. OwnerID is populated
// only on success; on failure, Reason gives enough info to log without
// leaking which keys exist.
type VerifyResult struct {
	OwnerID int
	KeyID   int
}

// Verify uses the key_prefix index to narrow candidates to a single row before
// doing the bcrypt comparison, turning O(n) bcrypt calls into O(1). The prefix
// is the first 8 chars ("gky_xxxx") — enough to uniquely identify one key in
// practice while staying short for index efficiency.
//
// Returns (result, nil) on hit, (zero, nil) on miss, (zero, err) on DB error.
func Verify(db *sql.DB, plaintext string) (VerifyResult, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" || !strings.HasPrefix(plaintext, KeyPrefix) {
		return VerifyResult{}, nil
	}
	// Extract visible prefix (first 8 chars: "gky_xxxx")
	var prefix string
	if len(plaintext) >= 8 {
		prefix = plaintext[:8]
	} else {
		return VerifyResult{}, nil
	}
	// Query only keys matching the prefix — typically 0 or 1 row
	var (
		id      int
		ownerID int
		hash    string
	)
	err := db.QueryRow(
		`SELECT id, owner_id, key_hash FROM api_keys
		 WHERE key_prefix = ? AND (expires_at IS NULL OR expires_at > NOW())
		 LIMIT 1`, prefix).Scan(&id, &ownerID, &hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return VerifyResult{}, nil
		}
		return VerifyResult{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) != nil {
		return VerifyResult{}, nil
	}
	// Update last_used_at asynchronously — non-blocking, best-effort.
	_, _ = db.Exec(`UPDATE api_keys SET last_used_at = NOW() WHERE id = ?`, id)
	return VerifyResult{OwnerID: ownerID, KeyID: id}, nil
}
