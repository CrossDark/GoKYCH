package schema

import (
	"database/sql"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/bcrypt"
)

// Init creates all tables (idempotent). Returns an error on the first DDL
// failure so the caller can halt startup instead of running with a broken schema.
func Init(db *sql.DB) error {
	for _, ddl := range allTables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("[schema] create table failed: %w", err)
		}
	}
	// Run migrations for columns/indexes that may be missing on databases
	// created before the column was added (CREATE TABLE IF NOT EXISTS does
	// not retroactively add columns).
	if err := runMigrations(db); err != nil {
		return fmt.Errorf("[schema] migration failed: %w", err)
	}
	slog.Info("all tables initialized")
	return nil
}

// runMigrations adds missing columns and indexes to existing tables.
// Each statement uses information_schema checks so it's safe to run
// multiple times without errors.
func runMigrations(db *sql.DB) error {
	migrations := []string{
		// comments.user_id — added to bind comments to logged-in users.
		`SELECT 1 FROM information_schema.COLUMNS
		  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'comments' AND COLUMN_NAME = 'user_id'
		  HAVING COUNT(*) = 0`,
		`ALTER TABLE comments ADD COLUMN user_id INT DEFAULT NULL AFTER line_number`,
		`ALTER TABLE comments ADD INDEX idx_comment_user (user_id)`,

		// ratings.user_id — added to bind ratings to logged-in users.
		`SELECT 1 FROM information_schema.COLUMNS
		  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'ratings' AND COLUMN_NAME = 'user_id'
		  HAVING COUNT(*) = 0`,
		`ALTER TABLE ratings ADD COLUMN user_id INT DEFAULT NULL AFTER article_id`,
		`ALTER TABLE ratings ADD INDEX idx_rating_user (user_id)`,

		// ratings.voter_key — deduplication key for ratings.
		`SELECT 1 FROM information_schema.COLUMNS
		  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'ratings' AND COLUMN_NAME = 'voter_key'
		  HAVING COUNT(*) = 0`,
		`ALTER TABLE ratings ADD COLUMN voter_key VARCHAR(141) NOT NULL DEFAULT 'n:匿名' AFTER author_name`,
	}
	for i := 0; i < len(migrations); i += 2 {
		check := migrations[i]
		stmt := migrations[i+1]
		var dummy int
		err := db.QueryRow(check).Scan(&dummy)
		if err == sql.ErrNoRows {
			// Column is missing — run the ALTER.
			if _, err := db.Exec(stmt); err != nil {
				slog.Warn("migration skipped (may already exist)", "stmt", stmt, "err", err)
			}
		}
	}
	return nil
}

// SeedAdmin inserts the default admin user if the users table is empty.
// If the user exists as admin, promotes to owner.
func SeedAdmin(db *sql.DB, username, password string) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		slog.Error("bcrypt hash failed", "err", err)
		return
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count)
	if err != nil {
		slog.Error("check admin", "err", err)
		return
	}

	if count == 0 {
		_, err = db.Exec(
			"INSERT INTO users (username, password_hash, nickname, role) VALUES (?, ?, ?, 'owner')",
			username, string(hash), username,
		)
		if err != nil {
			slog.Error("seed admin", "err", err)
			return
		}
		slog.Info("seeded admin user", "username", username)
	} else {
		// Promote admin → owner if needed.
		res, err := db.Exec(
			"UPDATE users SET role = 'owner' WHERE username = ? AND role = 'admin'",
			username,
		)
		if err != nil {
			slog.Error("promote admin", "err", err)
			return
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			slog.Info("promoted existing admin to owner", "username", username)
		}
	}
}

// allTables contains CREATE TABLE statements in dependency order.
// Tables with foreign keys must appear after their targets.
var allTables = [...]string{
	// ═══ 1. users ═══
	`CREATE TABLE IF NOT EXISTS users (
		id            INT AUTO_INCREMENT PRIMARY KEY,
		username      VARCHAR(64)  UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		nickname      VARCHAR(128) NOT NULL DEFAULT '',
		role          ENUM('user','admin','owner') NOT NULL DEFAULT 'user',
		avatar        VARCHAR(255) DEFAULT NULL,
		bio           VARCHAR(500) DEFAULT '',
		created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_username (username)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// ═══ 2. articles (unified from 5 separate tables) ═══
	`CREATE TABLE IF NOT EXISTS articles (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		type        ENUM('md','wikidot','html','bbcode','typst') NOT NULL,
		slug        VARCHAR(255) NOT NULL,
		title       VARCHAR(255) NOT NULL,
		content     LONGTEXT NOT NULL,
		author_id   INT DEFAULT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		UNIQUE KEY uq_type_slug (type, slug),
		INDEX idx_type_created (type, created_at),
		INDEX idx_author (author_id),
		FULLTEXT INDEX ft_title_content (title, content)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	// ═══ 3. tags ═══
	`CREATE TABLE IF NOT EXISTS tags (
		id   INT AUTO_INCREMENT PRIMARY KEY,
		name VARCHAR(64) UNIQUE NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 4. article_tags ═══
	`CREATE TABLE IF NOT EXISTS article_tags (
		article_id INT NOT NULL,
		tag_id     INT NOT NULL,
		PRIMARY KEY (article_id, tag_id),
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		FOREIGN KEY (tag_id)     REFERENCES tags(id)     ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 5. comments (full + line comments merged, line_number distinguishes) ═══
	`CREATE TABLE IF NOT EXISTS comments (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		article_id  INT NOT NULL,
		line_number INT DEFAULT NULL,
		user_id     INT DEFAULT NULL,
		author_name VARCHAR(128) NOT NULL DEFAULT '匿名',
		content     VARCHAR(500) NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		INDEX idx_article_line (article_id, line_number),
		INDEX idx_comment_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 6. ratings ═══
	`CREATE TABLE IF NOT EXISTS ratings (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		article_id  INT NOT NULL,
		user_id     INT DEFAULT NULL,
		author_name VARCHAR(128) NOT NULL,
		voter_key   VARCHAR(141) NOT NULL,
		score       DECIMAL(4,2) NOT NULL CHECK (score BETWEEN -1.00 AND 1.00),
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		UNIQUE KEY uq_user_article (article_id, voter_key),
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		INDEX idx_author_name (author_name),
		INDEX idx_rating_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 7. webauthn_credentials ═══
	`CREATE TABLE IF NOT EXISTS webauthn_credentials (
		id            INT AUTO_INCREMENT PRIMARY KEY,
		user_id       INT NOT NULL,
		credential_id VARBINARY(1024) NOT NULL UNIQUE,
		public_key    BLOB NOT NULL,
		sign_count    BIGINT DEFAULT 0,
		transports    VARCHAR(255) DEFAULT '',
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		INDEX idx_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 8. subsite_links ═══
	`CREATE TABLE IF NOT EXISTS subsite_links (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		name        VARCHAR(255) NOT NULL,
		url         VARCHAR(1024) NOT NULL,
		description VARCHAR(512) DEFAULT '',
		sort_order  INT NOT NULL DEFAULT 0,
		INDEX idx_sort (sort_order)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 9. featured_articles ═══
	`CREATE TABLE IF NOT EXISTS featured_articles (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		article_id  INT NOT NULL,
		sort_order  INT NOT NULL DEFAULT 0,
		UNIQUE KEY uq_featured (article_id),
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		INDEX idx_sort (sort_order)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 10. notifications ═══
	`CREATE TABLE IF NOT EXISTS notifications (
		id           INT AUTO_INCREMENT PRIMARY KEY,
		title        VARCHAR(255) NOT NULL,
		content      TEXT NOT NULL,
		is_important TINYINT(1) NOT NULL DEFAULT 0,
		is_active    TINYINT(1) NOT NULL DEFAULT 1,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_active_important (is_active, is_important)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 11. static_files ═══
	`CREATE TABLE IF NOT EXISTS static_files (
		id            INT AUTO_INCREMENT PRIMARY KEY,
		filename      VARCHAR(255) UNIQUE NOT NULL,
		original_name VARCHAR(255) NOT NULL,
		file_path     VARCHAR(512) NOT NULL,
		file_size     BIGINT NOT NULL DEFAULT 0,
		mime_type     VARCHAR(128) DEFAULT 'application/octet-stream',
		uploaded_by   INT DEFAULT NULL,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_uploaded_by (uploaded_by)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 12. typst_files ═══
	`CREATE TABLE IF NOT EXISTS typst_files (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		article_id  INT NOT NULL,
		filename    VARCHAR(255) NOT NULL,
		content     LONGTEXT NOT NULL,
		UNIQUE KEY uq_article_file (article_id, filename),
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 13. typst_cache ═══
	`CREATE TABLE IF NOT EXISTS typst_cache (
		id           INT AUTO_INCREMENT PRIMARY KEY,
		article_id   INT NOT NULL UNIQUE,
		html_content LONGTEXT NOT NULL,
		pdf_content  LONGBLOB NOT NULL,
		dependencies TEXT DEFAULT NULL,
		compiled_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 14. api_keys ═══
	//
	// API keys let admins authenticate to the Go backend without a session
	// cookie (useful for plugins, scripts, CI). The plaintext key is only
	// returned ONCE at creation; we store its bcrypt hash + the visible
	// prefix (first 8 chars) so the admin can identify the key in the
	// list view without ever seeing the secret half again.
	`CREATE TABLE IF NOT EXISTS api_keys (
		id           INT AUTO_INCREMENT PRIMARY KEY,
		owner_id     INT NOT NULL,
		name         VARCHAR(128) NOT NULL,
		key_prefix   VARCHAR(16) NOT NULL,
		key_hash     VARCHAR(255) NOT NULL,
		last_used_at DATETIME DEFAULT NULL,
		expires_at   DATETIME DEFAULT NULL,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE,
		INDEX idx_owner (owner_id),
		INDEX idx_prefix (key_prefix)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
}
