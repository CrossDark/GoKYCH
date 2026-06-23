package content

import (
	"database/sql"
	"time"
)

// UserSummary is the non-sensitive view of a user for admin listings.
type UserSummary struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// ListUsers returns all users, newest first. Admin-only by callers.
func ListUsers(db *sql.DB) ([]UserSummary, error) {
	rows, err := db.Query(
		`SELECT id, username, nickname, role, created_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UserSummary, 0)
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AdminNotification is a notification row with admin-only fields (is_active).
// The public Notification type (used by /api/notifications) omits is_active.
type AdminNotification struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	IsImportant bool      `json:"is_important"`
	IsActive    bool      `json:"is_active"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListAdminNotifications returns ALL notifications (incl. inactive), newest first.
func ListAdminNotifications(db *sql.DB) ([]AdminNotification, error) {
	rows, err := db.Query(
		`SELECT id, title, content, is_important, is_active, updated_at
		 FROM notifications ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AdminNotification, 0)
	for rows.Next() {
		var n AdminNotification
		var imp, act int
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &imp, &act, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.IsImportant = imp == 1
		n.IsActive = act == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

// StaticFile is a row of the static_files table for admin listings.
type StaticFile struct {
	ID           int       `json:"id"`
	Filename     string    `json:"filename"`
	OriginalName string    `json:"original_name"`
	FileSize     int64     `json:"file_size"`
	MimeType     string    `json:"mime_type"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListStaticFiles returns uploaded static files, newest first.
func ListStaticFiles(db *sql.DB) ([]StaticFile, error) {
	rows, err := db.Query(
		`SELECT id, filename, original_name, file_size, mime_type, created_at
		 FROM static_files ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StaticFile, 0)
	for rows.Next() {
		var f StaticFile
		if err := rows.Scan(&f.ID, &f.Filename, &f.OriginalName, &f.FileSize, &f.MimeType, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
