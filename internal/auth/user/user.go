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

// scanUser scans the common 5 NullString fields (avatar, bio, social_email,
// social_github, social_qq) into u, used by all three Get* functions so the
// 20-line scan block isn't copy-pasted. The remaining non-nullable columns
// (id, username, nickname, role, created_at) are scanned by the caller's
// explicit Scan call — this helper handles only the optional block.
func scanUser(u *User, avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString) {
	u.Avatar = avatar.String
	u.Bio = bio.String
	u.SocialEmail = socialEmail.String
	u.SocialGithub = socialGithub.String
	u.SocialQQ = socialQQ.String
}

// GetByUsernameCtx loads a user (without password hash) by username.
func GetByUsernameCtx(ctx context.Context, db *sql.DB, username string) (*User, error) {
	u := &User{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	scanUser(u, avatar, bio, socialEmail, socialGithub, socialQQ)
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
	err := db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	scanUser(u, avatar, bio, socialEmail, socialGithub, socialQQ)
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
	err := db.QueryRowContext(ctx,
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at, password_hash
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt, &u.PasswordHash)
	if err != nil {
		return nil, err
	}
	scanUser(&u.User, avatar, bio, socialEmail, socialGithub, socialQQ)
	return u, nil
}

// Deprecated: Use GetWithPasswordCtx instead.
func GetWithPassword(db *sql.DB, username string) (*UserWithPassword, error) {
	return GetWithPasswordCtx(context.TODO(), db, username)
}

// ListCtx returns all users ordered by created_at desc.
func ListCtx(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, username, nickname, role, created_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out = make([]User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
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

// NormalizeUsername trims surrounding whitespace from a username.
func NormalizeUsername(s string) string { return strings.TrimSpace(s) }
