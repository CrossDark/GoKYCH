package user

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Role constants (hierarchy: user < admin < owner).
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
	RoleOwner = "owner"
)

var ValidRoles = []string{RoleUser, RoleAdmin, RoleOwner}

// User represents a user record without the password hash.
type User struct {
	ID           int       `json:"id"`
	Username     string    `json:"username"`
	Nickname     string    `json:"nickname"`
	Role         string    `json:"role"`
	Avatar       string    `json:"avatar"`
	Bio          string    `json:"bio"`
	SocialEmail  string    `json:"social_email"`
	SocialGithub string    `json:"social_github"`
	SocialQQ     string    `json:"social_qq"`
	CreatedAt    time.Time `json:"created_at"`
	// MustResetPassword is true when an admin/owner has triggered a
	// "force-reset on next login" — the next successful login (with the
	// current credentials) atomically rotates the password and surfaces
	// the new plaintext to the user via the login response. Cleared by
	// the login handler when the rotation fires.
	MustResetPassword bool `json:"must_reset_password"`
	// SessionInvalidatedAt is a moving "credentials/invalidation unix
	// timestamp" — any session whose login_time is earlier than this
	// value is treated as expired. Used to forcibly log out a user
	// after an admin/owner-initiated password reset or force-logout
	// action, and after a self-service password change. JSON-hidden
	// because it's pure server-side bookkeeping; the value is read by
	// session.Manager.CurrentUserCtx, never returned over the wire.
	// Stored as a Unix int (not a DATETIME) so the comparison is
	// timezone-agnostic — see schema.go for why we don't trust
	// MySQL DATETIME <-> time.Time round-tripping.
	SessionInvalidatedAt int64 `json:"-"`
}

// Exists reports whether an error is sql.ErrNoRows (user not found).
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// IsValidRole reports whether r is a recognized role.
func IsValidRole(r string) bool {
	for _, v := range ValidRoles {
		if v == r {
			return true
		}
	}
	return false
}

// IsAdmin reports whether the role is admin or owner.
func IsAdmin(role string) bool { return role == RoleAdmin || role == RoleOwner }

// IsOwner reports whether the role is owner.
func IsOwner(role string) bool { return role == RoleOwner }

// scanUser scans the common per-row fields (avatar/bio/social_* as
// NullStrings, must_reset_password as int from TINYINT, and
// session_invalidated_at as a nullable INT) into u. Used by all
// Get* functions so the long scan block isn't copy-pasted; the
// caller still does the explicit Scan for the non-nullable columns
// (id, username, nickname, role, created_at, password_hash).
func scanUser(u *User, avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString, mustReset int, sessionInvalidatedAt sql.NullInt64) {
	u.Avatar = avatar.String
	u.Bio = bio.String
	u.SocialEmail = socialEmail.String
	u.SocialGithub = socialGithub.String
	u.SocialQQ = socialQQ.String
	u.MustResetPassword = mustReset != 0
	if sessionInvalidatedAt.Valid {
		u.SessionInvalidatedAt = sessionInvalidatedAt.Int64
	}
}

// GetByUsernameCtx loads a user (without password hash) by username.
func GetByUsernameCtx(ctx context.Context, db *sql.DB, username string) (*User, error) {
	u := &User{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	var mustReset int
	var sessionInvalidatedAt sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at,
		        must_reset_password, session_invalidated_at
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt,
		&mustReset, &sessionInvalidatedAt)
	if err != nil {
		return nil, err
	}
	scanUser(u, avatar, bio, socialEmail, socialGithub, socialQQ, mustReset, sessionInvalidatedAt)
	return u, nil
}

// Deprecated: Use GetByUsernameCtx instead.
func GetByUsername(db *sql.DB, username string) (*User, error) {
	return GetByUsernameCtx(context.TODO(), db, username)
}

// GetByIDCtx loads a user by id.
func GetByIDCtx(ctx context.Context, db *sql.DB, id int) (*User, error) {
	u := &User{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	var mustReset int
	var sessionInvalidatedAt sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at,
		        must_reset_password, session_invalidated_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt,
		&mustReset, &sessionInvalidatedAt)
	if err != nil {
		return nil, err
	}
	scanUser(u, avatar, bio, socialEmail, socialGithub, socialQQ, mustReset, sessionInvalidatedAt)
	return u, nil
}

// Deprecated: Use GetByIDCtx instead.
func GetByID(db *sql.DB, id int) (*User, error) {
	return GetByIDCtx(context.TODO(), db, id)
}

// GetWithPassword loads the password_hash alongside user fields (login flow only).
type UserWithPassword struct {
	User
	PasswordHash string `json:"-"`
}

func GetWithPasswordCtx(ctx context.Context, db *sql.DB, username string) (*UserWithPassword, error) {
	u := &UserWithPassword{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	var mustReset int
	var sessionInvalidatedAt sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at,
		        must_reset_password, session_invalidated_at, password_hash
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt,
		&mustReset, &sessionInvalidatedAt, &u.PasswordHash)
	if err != nil {
		return nil, err
	}
	scanUser(&u.User, avatar, bio, socialEmail, socialGithub, socialQQ, mustReset, sessionInvalidatedAt)
	return u, nil
}

// Deprecated: Use GetWithPasswordCtx instead.
func GetWithPassword(db *sql.DB, username string) (*UserWithPassword, error) {
	return GetWithPasswordCtx(context.TODO(), db, username)
}

// ListCtx returns all users ordered by created_at desc.
func ListCtx(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, username, nickname, role, created_at,
		        must_reset_password, session_invalidated_at
		 FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out = make([]User, 0)
	for rows.Next() {
		var u User
		var mustReset int
		var sessionInvalidatedAt sql.NullInt64
		if err := rows.Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.CreatedAt,
			&mustReset, &sessionInvalidatedAt); err != nil {
			return nil, err
		}
		u.MustResetPassword = mustReset != 0
		if sessionInvalidatedAt.Valid {
			u.SessionInvalidatedAt = sessionInvalidatedAt.Int64
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Deprecated: Use ListCtx instead.
func List(db *sql.DB) ([]User, error) {
	return ListCtx(context.TODO(), db)
}

// CreateCtx inserts a new user. nickname falls back to username if empty.
func CreateCtx(ctx context.Context, db *sql.DB, username, passwordHash, nickname, role string) (int64, error) {
	if !IsValidRole(role) {
		return 0, errors.New("无效的角色: " + role + "，有效值: user, admin, owner")
	}
	if nickname == "" {
		nickname = username
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, nickname, role) VALUES (?, ?, ?, ?)`,
		username, passwordHash, nickname, role)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Deprecated: Use CreateCtx instead.
func Create(db *sql.DB, username, passwordHash, nickname, role string) (int64, error) {
	return CreateCtx(context.TODO(), db, username, passwordHash, nickname, role)
}

// UpdatePasswordCtx sets a new password hash.
func UpdatePasswordCtx(ctx context.Context, db *sql.DB, username, passwordHash string) (bool, error) {
	res, err := db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`,
		passwordHash, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Deprecated: Use UpdatePasswordCtx instead.
func UpdatePassword(db *sql.DB, username, passwordHash string) (bool, error) {
	return UpdatePasswordCtx(context.TODO(), db, username, passwordHash)
}

// UpdateInfoCtx updates nickname and role. An empty nickname is treated as
// "leave the existing value alone" — otherwise a role-only mutation
// (e.g. updateUserRole) would silently wipe out a user's display name
// by passing nickname="". Callers that genuinely want to clear the
// nickname should bypass this and write a dedicated handler.
func UpdateInfoCtx(ctx context.Context, db *sql.DB, username, nickname, role string) (bool, error) {
	if !IsValidRole(role) {
		return false, errors.New("无效的角色: " + role)
	}
	var (
		res sql.Result
		err error
	)
	if nickname == "" {
		res, err = db.ExecContext(ctx, `UPDATE users SET role = ? WHERE username = ?`,
			role, username)
	} else {
		res, err = db.ExecContext(ctx, `UPDATE users SET nickname = ?, role = ? WHERE username = ?`,
			nickname, role, username)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Deprecated: Use UpdateInfoCtx instead.
func UpdateInfo(db *sql.DB, username, nickname, role string) (bool, error) {
	return UpdateInfoCtx(context.TODO(), db, username, nickname, role)
}

// UpdateProfileCtx updates avatar, bio, and per-user social links (self-service,
// no role change). social fields are plain strings — an empty string clears
// the value to NULL so the frontend can render an "unset" state.
func UpdateProfileCtx(ctx context.Context, db *sql.DB, userID int, avatar, bio, socialEmail, socialGithub, socialQQ string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE users
		 SET avatar = ?, bio = ?,
		     social_email = NULLIF(?, ''),
		     social_github = NULLIF(?, ''),
		     social_qq = NULLIF(?, '')
		 WHERE id = ?`,
		avatar, bio, socialEmail, socialGithub, socialQQ, userID,
	)
	return err
}

// Deprecated: Use UpdateProfileCtx instead.
func UpdateProfile(db *sql.DB, userID int, avatar, bio, socialEmail, socialGithub, socialQQ string) error {
	return UpdateProfileCtx(context.TODO(), db, userID, avatar, bio, socialEmail, socialGithub, socialQQ)
}

// DeleteCtx removes a user by username. Related data is cleaned by FK CASCADE
// for owned articles/comments-by-author_name are NOT cascaded — callers handle.
func DeleteCtx(ctx context.Context, db *sql.DB, username string) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM users WHERE username = ?`, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Deprecated: Use DeleteCtx instead.
func Delete(db *sql.DB, username string) (bool, error) {
	return DeleteCtx(context.TODO(), db, username)
}

// SetMustResetPasswordCtx sets the per-user "force-reset on next login"
// flag. Used by the admin/owner "强制重置密码" action — a non-zero
// value flips the flag on, zero flips it off. Returns true if the row
// was found and updated.
func SetMustResetPasswordCtx(ctx context.Context, db *sql.DB, username string, on bool) (bool, error) {
	v := 0
	if on {
		v = 1
	}
	res, err := db.ExecContext(ctx, `UPDATE users SET must_reset_password = ? WHERE username = ?`, v, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Deprecated: Use SetMustResetPasswordCtx instead.
func SetMustResetPassword(db *sql.DB, username string, on bool) (bool, error) {
	return SetMustResetPasswordCtx(context.TODO(), db, username, on)
}

// BumpSessionInvalidatedAtCtx updates the user's "credentials/invalidation
// timestamp" to time.Now().Unix(), invalidating any session whose
// login_time is older. Called by the password-rotation flows (force-reset,
// immediate-reset, self-service change) and by the force-logout action.
// Returns true on a successful row match.
//
// The Go-side timestamp (not MySQL's NOW()) is used so the column
// holds a Unix-seconds value that's directly comparable to the
// session's login_time — see schema.go for why we don't trust
// DATETIME round-tripping.
func BumpSessionInvalidatedAtCtx(ctx context.Context, db *sql.DB, username string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET session_invalidated_at = ? WHERE username = ?`,
		time.Now().Unix(), username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Deprecated: Use BumpSessionInvalidatedAtCtx instead.
func BumpSessionInvalidatedAt(db *sql.DB, username string) (bool, error) {
	return BumpSessionInvalidatedAtCtx(context.TODO(), db, username)
}

// RotatePasswordCtx atomically: bumps session_invalidated_at, hashes the
// new password, and writes it. Clears must_reset_password so a subsequent
// login won't trigger another rotation. Returns true if a row was updated.
// Used by both the immediate-reset endpoint (admin sees the new plaintext,
// and we want to kick the user out so they re-login with the new pwd) and
// the changeMyPassword self-service path (also wants to kick other tabs).
//
// session_invalidated_at is now a Unix-seconds INT (not DATETIME), so we
// pass time.Now().Unix() rather than using MySQL's NOW() — see schema.go.
func RotatePasswordCtx(ctx context.Context, db *sql.DB, username, newPasswordHash string, clearMustReset bool) (bool, error) {
	now := time.Now().Unix()
	if clearMustReset {
		res, err := db.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, must_reset_password = 0, session_invalidated_at = ? WHERE username = ?`,
			newPasswordHash, now, username)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		return n > 0, err
	}
	res, err := db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, session_invalidated_at = ? WHERE username = ?`,
		newPasswordHash, now, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RotatePasswordForLoginCtx updates password_hash and clears
// must_reset_password, but DELIBERATELY does NOT bump
// session_invalidated_at. This is the variant called by postLogin
// when a user authenticates with a flagged account — the new
// session is created milliseconds later, and bumping
// session_invalidated_at right before that would risk the new
// session's login_time being interpreted as stale (depending on
// MySQL vs Go clock skew). For the login auto-rotation, there's
// no other session to invalidate anyway — the user was already
// logged out by the admin's force-reset action that set the flag.
func RotatePasswordForLoginCtx(ctx context.Context, db *sql.DB, username, newPasswordHash string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_reset_password = 0 WHERE username = ?`,
		newPasswordHash, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// NormalizeUsername trims surrounding whitespace from a username.
func NormalizeUsername(s string) string { return strings.TrimSpace(s) }
// timestamp
