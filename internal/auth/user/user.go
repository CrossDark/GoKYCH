package user

import (
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

// GetByUsername loads a user (without password hash) by username.
func GetByUsername(db *sql.DB, username string) (*User, error) {
	u := &User{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	err := db.QueryRow(
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.Avatar = avatar.String
	u.Bio = bio.String
	u.SocialEmail = socialEmail.String
	u.SocialGithub = socialGithub.String
	u.SocialQQ = socialQQ.String
	return u, nil
}

// GetByID loads a user by id.
func GetByID(db *sql.DB, id int) (*User, error) {
	u := &User{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	err := db.QueryRow(
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.Avatar = avatar.String
	u.Bio = bio.String
	u.SocialEmail = socialEmail.String
	u.SocialGithub = socialGithub.String
	u.SocialQQ = socialQQ.String
	return u, nil
}

// GetWithPassword loads the password_hash alongside user fields (login flow only).
type UserWithPassword struct {
	User
	PasswordHash string `json:"-"`
}

func GetWithPassword(db *sql.DB, username string) (*UserWithPassword, error) {
	u := &UserWithPassword{}
	var avatar, bio, socialEmail, socialGithub, socialQQ sql.NullString
	err := db.QueryRow(
		`SELECT id, username, nickname, role, avatar, bio,
		        social_email, social_github, social_qq, created_at, password_hash
		 FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &avatar, &bio,
		&socialEmail, &socialGithub, &socialQQ, &u.CreatedAt, &u.PasswordHash)
	if err != nil {
		return nil, err
	}
	u.Avatar = avatar.String
	u.Bio = bio.String
	u.SocialEmail = socialEmail.String
	u.SocialGithub = socialGithub.String
	u.SocialQQ = socialQQ.String
	return u, nil
}

// List returns all users ordered by created_at desc.
func List(db *sql.DB) ([]User, error) {
	rows, err := db.Query(
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

// Create inserts a new user. nickname falls back to username if empty.
func Create(db *sql.DB, username, passwordHash, nickname, role string) (int64, error) {
	if !IsValidRole(role) {
		return 0, errors.New("无效的角色: " + role + "，有效值: user, admin, owner")
	}
	if nickname == "" {
		nickname = username
	}
	res, err := db.Exec(
		`INSERT INTO users (username, password_hash, nickname, role) VALUES (?, ?, ?, ?)`,
		username, passwordHash, nickname, role)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePassword sets a new password hash.
func UpdatePassword(db *sql.DB, username, passwordHash string) (bool, error) {
	res, err := db.Exec(`UPDATE users SET password_hash = ? WHERE username = ?`,
		passwordHash, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateInfo updates nickname and role. An empty nickname is treated as
// "leave the existing value alone" — otherwise a role-only mutation
// (e.g. updateUserRole) would silently wipe out a user's display name
// by passing nickname="". Callers that genuinely want to clear the
// nickname should bypass this and write a dedicated handler.
func UpdateInfo(db *sql.DB, username, nickname, role string) (bool, error) {
	if !IsValidRole(role) {
		return false, errors.New("无效的角色: " + role)
	}
	var (
		res sql.Result
		err error
	)
	if nickname == "" {
		res, err = db.Exec(`UPDATE users SET role = ? WHERE username = ?`,
			role, username)
	} else {
		res, err = db.Exec(`UPDATE users SET nickname = ?, role = ? WHERE username = ?`,
			nickname, role, username)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UpdateProfile updates avatar, bio, and per-user social links (self-service,
// no role change). social fields are plain strings — an empty string clears
// the value to NULL so the frontend can render an "unset" state.
func UpdateProfile(db *sql.DB, userID int, avatar, bio, socialEmail, socialGithub, socialQQ string) error {
	_, err := db.Exec(
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

// Delete removes a user by username. Related data is cleaned by FK CASCADE
// for owned articles/comments-by-author_name are NOT cascaded — callers handle.
func Delete(db *sql.DB, username string) (bool, error) {
	res, err := db.Exec(`DELETE FROM users WHERE username = ?`, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// NormalizeUsername trims surrounding whitespace from a username.
func NormalizeUsername(s string) string { return strings.TrimSpace(s) }
