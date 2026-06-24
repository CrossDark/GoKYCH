// Package passkey wraps the go-webauthn library to fit the existing GoKYCH
// user model and the existing webauthn_credentials table.
//
// Wire format on the row:
//
//	id              — auto-inc
//	user_id         — FK to users.id (delete with the user)
//	name            — user-chosen label for the credential (e.g. "MacBook
//	                 Touch ID"). Multiple credentials per user are allowed;
//	                 the list view in /admin/passkeys shows each one.
//	credential_id   — base64url(webauthn.Credential.ID), UNIQUE
//	public_key      — base64url(COSE-encoded public key bytes)
//	sign_count      — uint32 monotonic counter (anti-cloning)
//	transports      — comma-separated hint list from the authenticator
//	created_at      — DATETIME
//
// The go-webauthn library's Credential struct is the canonical
// representation; we only persist the bytes we need and rehydrate a
// Credential on read.
package passkey

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Credential is the on-disk + JSON representation of a stored passkey.
type Credential struct {
	ID           int64     `json:"id"`
	UserID       int       `json:"user_id"`
	Name         string    `json:"name"`
	CredentialID string    `json:"credential_id"` // base64url, opaque
	Transports   []string  `json:"transports"`
	SignCount    uint32    `json:"sign_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// User is the implementation of webauthn.User that the go-webauthn lib
// needs. It holds a snapshot of the user record at the start of a
// registration / login ceremony and a list of existing credentials.
type User struct {
	ID          int
	Username    string
	DisplayName string
	// webAuthnID is the stable user handle sent in the WebAuthn protocol.
	// We use the DB id encoded as a string, which is unique and never
	// changes (unlike username, which admins can rename).
	webAuthnID []byte
	// Credentials are the user's existing passkeys. webauthn lib uses
	// this list to determine which credentials the authenticator is
	// allowed to use (preventing one user logging in as another by
	// presenting a known credential id).
	Credentials []webauthn.Credential
}

// WebAuthnID implements webauthn.User. Stable per-user byte slice sent in
// the protocol — never changes even if the username is renamed.
func (u *User) WebAuthnID() []byte { return u.webAuthnID }
func (u *User) WebAuthnName() string {
	return u.Username
}
func (u *User) WebAuthnDisplayName() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// LoadUser returns a webauthn.User with the user's existing credentials
// attached, ready to be passed to BeginRegistration. Pass an empty
// userID to return an empty user (used in the discoverable-login path
// where the user is looked up from the credential id).
func LoadUser(db *sql.DB, userID int) (*User, error) {
	u := &User{ID: userID}
	var nickname sql.NullString
	err := db.QueryRow(`SELECT username, nickname FROM users WHERE id = ?`, userID).Scan(&u.Username, &nickname)
	if err != nil {
		return nil, err
	}
	if nickname.Valid {
		u.DisplayName = nickname.String
	}
	u.webAuthnID = []byte(fmt.Sprintf("gokych-user-%d", userID))
	creds, err := loadCredentials(db, userID)
	if err != nil {
		return nil, err
	}
	u.Credentials = creds
	return u, nil
}

// ListForUser returns all credentials owned by userID (most recent first),
// without the raw public-key material.
func ListForUser(db *sql.DB, userID int) ([]Credential, error) {
	rows, err := db.Query(
		`SELECT id, user_id, name, credential_id, transports, sign_count, created_at
		 FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Credential, 0)
	for rows.Next() {
		var c Credential
		var transports sql.NullString
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.CredentialID, &transports, &c.SignCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		if transports.Valid && transports.String != "" {
			c.Transports = strings.Split(transports.String, ",")
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCredential persists a freshly-registered credential. The Credential
// struct comes from webauthn lib's FinishRegistration output.
func SaveCredential(db *sql.DB, userID int, name string, c *webauthn.Credential) error {
	if c == nil {
		return errors.New("nil credential")
	}
	if strings.TrimSpace(name) == "" {
		name = "未命名 Passkey"
	}
	if len(name) > 128 {
		name = name[:128]
	}
	credB64 := base64.RawURLEncoding.EncodeToString(c.ID)
	pubB64 := base64.RawURLEncoding.EncodeToString(c.PublicKey)
	transportStrs := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		transportStrs = append(transportStrs, string(t))
	}
	transports := strings.Join(transportStrs, ",")
	_, err := db.Exec(
		`INSERT INTO webauthn_credentials
		 (user_id, name, credential_id, public_key, sign_count, transports)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		userID, name, credB64, pubB64, c.Authenticator.SignCount, transports)
	return err
}

// Delete removes a credential by id, scoped to userID. Returns true when a
// row was actually deleted.
func Delete(db *sql.DB, userID int, credentialDBID int64) (bool, error) {
	res, err := db.Exec(
		`DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`,
		credentialDBID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// HasAny reports whether userID has at least one passkey registered.
// Used by the login flow to gate password login: if the user has a
// passkey, passwords are disabled (except for the owner, who is exempt
// to avoid lockout).
func HasAny(db *sql.DB, userID int) (bool, error) {
	var c int
	err := db.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, userID).Scan(&c)
	return c > 0, err
}

// loadCredentials returns the user's stored passkeys as webauthn.Credential
// values (the form expected by webauthn lib's User interface).
func loadCredentials(db *sql.DB, userID int) ([]webauthn.Credential, error) {
	rows, err := db.Query(
		`SELECT credential_id, public_key, sign_count, transports
		 FROM webauthn_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]webauthn.Credential, 0)
	for rows.Next() {
		var credB64, pubB64, transports string
		var signCount uint32
		if err := rows.Scan(&credB64, &pubB64, &signCount, &transports); err != nil {
			return nil, err
		}
		credID, err := base64.RawURLEncoding.DecodeString(credB64)
		if err != nil {
			continue // skip corrupted rows
		}
		pubKey, err := base64.RawURLEncoding.DecodeString(pubB64)
		if err != nil {
			continue
		}
		out = append(out, webauthn.Credential{
			ID:        credID,
			PublicKey: pubKey,
			Authenticator: webauthn.Authenticator{
				SignCount: signCount,
			},
			Transport: transportsToAuthenticatorTransports(splitNonEmpty(transports, ",")),
		})
	}
	return out, rows.Err()
}

// LookupByCredentialID is the discoverable-login resolver. When a
// navigator.credentials.get() call returns an assertion, the
// authenticator only sent the credential_id (no username). The
// webauthn lib calls this with that id to find the owning user.
func LookupByCredentialID(db *sql.DB, credID []byte) (*User, error) {
	credB64 := base64.RawURLEncoding.EncodeToString(credID)
	var userID int
	err := db.QueryRow(
		`SELECT user_id FROM webauthn_credentials WHERE credential_id = ? LIMIT 1`,
		credB64).Scan(&userID)
	if err != nil {
		return nil, err
	}
	return LoadUser(db, userID)
}

// PersistSignCount updates the sign_count after a successful assertion.
// The webauthn lib detects cloning by tracking that the counter strictly
// increases across uses for the same credential.
func PersistSignCount(db *sql.DB, credID []byte, newCount uint32) error {
	credB64 := base64.RawURLEncoding.EncodeToString(credID)
	_, err := db.Exec(`UPDATE webauthn_credentials SET sign_count = ? WHERE credential_id = ?`, newCount, credB64)
	return err
}

// splitNonEmpty is a tiny helper for the comma-separated transports list.
func splitNonEmpty(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// transportsToAuthenticatorTransports converts plain strings (e.g. "usb",
// "nfc", "internal") to the webauthn lib's transport type.
func transportsToAuthenticatorTransports(s []string) []protocol.AuthenticatorTransport {
	out := make([]protocol.AuthenticatorTransport, 0, len(s))
	for _, t := range s {
		out = append(out, protocol.AuthenticatorTransport(t))
	}
	return out
}

// OriginFromRequest pulls the Origin / Referer header off the request,
// used to populate WebAuthn.RPOrigins at config time. The list grows
// over time; the caller decides how to scope it.
func OriginFromRequest(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" {
		return o
	}
	// Fallback: scheme + host.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// Ensure registry is in sync with webauthn lib's expectations.
var _ webauthn.User = (*User)(nil)
var _ protocol.AuthenticatorAttachment
