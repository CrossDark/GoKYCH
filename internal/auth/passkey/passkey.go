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
	"context"
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

// LoadUserCtx returns a webauthn.User with the user's existing credentials
// attached, ready to be passed to BeginRegistration. Pass an empty
// userID to return an empty user (used in the discoverable-login path
// where the user is looked up from the credential id).
func LoadUserCtx(ctx context.Context, db *sql.DB, userID int) (*User, error) {
	u := &User{ID: userID}
	var nickname sql.NullString
	err := db.QueryRowContext(ctx, `SELECT username, nickname FROM users WHERE id = ?`, userID).Scan(&u.Username, &nickname)
	if err != nil {
		return nil, err
	}
	if nickname.Valid {
		u.DisplayName = nickname.String
	}
	u.webAuthnID = []byte(fmt.Sprintf("gokych-user-%d", userID))
	creds, err := loadCredentialsCtx(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	u.Credentials = creds
	return u, nil
}

// Deprecated: Use LoadUserCtx instead.
func LoadUser(db *sql.DB, userID int) (*User, error) {
	return LoadUserCtx(context.TODO(), db, userID)
}

// ListForUserCtx returns all credentials owned by userID (most recent first),
// without the raw public-key material.
func ListForUserCtx(ctx context.Context, db *sql.DB, userID int) ([]Credential, error) {
	rows, err := db.QueryContext(ctx,
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

// Deprecated: Use ListForUserCtx instead.
func ListForUser(db *sql.DB, userID int) ([]Credential, error) {
	return ListForUserCtx(context.TODO(), db, userID)
}

// SaveCredentialCtx persists a freshly-registered credential. The Credential
// struct comes from webauthn lib's FinishRegistration output.
//
// backup_eligible is set from c.Flags.BackupEligible. The go-webauthn lib
// compares that stored value against the authenticator-data flags on every
// login and rejects the assertion with "Backup Eligible flag inconsistency"
// if it ever flips — so we MUST persist it now, otherwise every credential
// registered by an authenticator that reports BE=1 (i.e. anything that
// supports cloud sync or device transfer) fails to log in.
func SaveCredentialCtx(ctx context.Context, db *sql.DB, userID int, name string, c *webauthn.Credential) error {
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
	be := 0
	if c.Flags.BackupEligible {
		be = 1
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO webauthn_credentials
		 (user_id, name, credential_id, public_key, sign_count, transports, backup_eligible)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, name, credB64, pubB64, c.Authenticator.SignCount, transports, be)
	return err
}

// Deprecated: Use SaveCredentialCtx instead.
func SaveCredential(db *sql.DB, userID int, name string, c *webauthn.Credential) error {
	return SaveCredentialCtx(context.TODO(), db, userID, name, c)
}

// DeleteCtx removes a credential by id, scoped to userID. Returns true when a
// row was actually deleted.
func DeleteCtx(ctx context.Context, db *sql.DB, userID int, credentialDBID int64) (bool, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`,
		credentialDBID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Deprecated: Use DeleteCtx instead.
func Delete(db *sql.DB, userID int, credentialDBID int64) (bool, error) {
	return DeleteCtx(context.TODO(), db, userID, credentialDBID)
}

// HasAnyCtx reports whether userID has at least one passkey registered.
// Used by the login flow to gate password login: if the user has a
// passkey, passwords are disabled (except for the owner, who is exempt
// to avoid lockout).
func HasAnyCtx(ctx context.Context, db *sql.DB, userID int) (bool, error) {
	var c int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, userID).Scan(&c)
	return c > 0, err
}

// Deprecated: Use HasAnyCtx instead.
func HasAny(db *sql.DB, userID int) (bool, error) {
	return HasAnyCtx(context.TODO(), db, userID)
}

// loadCredentialsCtx returns the user's stored passkeys as webauthn.Credential
// values (the form expected by webauthn lib's User interface).
//
// We restore Flags.BackupEligible from the stored column. Without it the
// lib sees a zero-value Flags struct (BE=false) and rejects every login
// from an authenticator that reported BE=true at registration — see
// SaveCredential's comment for why this matters.
func loadCredentialsCtx(ctx context.Context, db *sql.DB, userID int) ([]webauthn.Credential, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT credential_id, public_key, sign_count, transports, backup_eligible
		 FROM webauthn_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]webauthn.Credential, 0)
	for rows.Next() {
		var credB64, pubB64, transports string
		var signCount uint32
		var backupEligible int
		if err := rows.Scan(&credB64, &pubB64, &signCount, &transports, &backupEligible); err != nil {
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
			Flags:     webauthn.CredentialFlags{BackupEligible: backupEligible != 0},
			Authenticator: webauthn.Authenticator{
				SignCount: signCount,
			},
			Transport: transportsToAuthenticatorTransports(splitNonEmpty(transports, ",")),
		})
	}
	return out, rows.Err()
}

// LookupByCredentialIDCtx is the discoverable-login resolver. When a
// navigator.credentials.get() call returns an assertion, the
// authenticator only sent the credential_id (no username). The
// webauthn lib calls this with that id to find the owning user.
//
// The "credential_id not found" path is wrapped in a sentinel error so
// the API layer can translate it into a friendlier Chinese message —
// otherwise the lib just says "Failed to lookup Client-side Discoverable
// Credential: sql: no rows in result set" which leaves the user guessing
// (this commonly happens when their browser cached a passkey that was
// later revoked from the server).
func LookupByCredentialIDCtx(ctx context.Context, db *sql.DB, credID []byte) (*User, error) {
	credB64 := base64.RawURLEncoding.EncodeToString(credID)
	var userID int
	err := db.QueryRowContext(ctx,
		`SELECT user_id FROM webauthn_credentials WHERE credential_id = ? LIMIT 1`,
		credB64).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}
	return LoadUserCtx(ctx, db, userID)
}

// Deprecated: Use LookupByCredentialIDCtx instead.
func LookupByCredentialID(db *sql.DB, credID []byte) (*User, error) {
	return LookupByCredentialIDCtx(context.TODO(), db, credID)
}

// ErrCredentialNotFound is returned by LookupByCredentialID when the
// authenticator presented a credential_id that no longer exists in the
// webauthn_credentials table — typically because the server-side passkey
// was revoked but the browser / password manager still has it cached.
var ErrCredentialNotFound = errors.New("passkey credential not found")

// PersistSignCountCtx updates the sign_count after a successful assertion.
// The webauthn lib detects cloning by tracking that the counter strictly
// increases across uses for the same credential.
func PersistSignCountCtx(ctx context.Context, db *sql.DB, credID []byte, newCount uint32) error {
	credB64 := base64.RawURLEncoding.EncodeToString(credID)
	_, err := db.ExecContext(ctx, `UPDATE webauthn_credentials SET sign_count = ? WHERE credential_id = ?`, newCount, credB64)
	return err
}

// Deprecated: Use PersistSignCountCtx instead.
func PersistSignCount(db *sql.DB, credID []byte, newCount uint32) error {
	return PersistSignCountCtx(context.TODO(), db, credID, newCount)
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
