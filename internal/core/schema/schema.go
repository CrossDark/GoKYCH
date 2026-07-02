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

// runMigrations adds missing columns to existing tables that were
// created before a feature was added. CREATE TABLE IF NOT EXISTS does
// not retroactively add new columns, so an explicit ALTER is required
// for live upgrades.
//
// Each migration runs a parameterised COUNT against information_schema
// first; if the column already exists (n > 0) we skip the ALTER. This is
// the standard MySQL pattern — `SELECT ... HAVING COUNT(*) = 0` is
// unreliable when the WHERE matches rows in OTHER tables that share
// the same column name, so we use a plain scalar count.
func runMigrations(db *sql.DB) error {
	// (table, column, alter) — column presence is the trigger.
	migrations := []struct {
		table  string
		column string
		alter  string
	}{
		{"comments", "user_id", "ALTER TABLE comments ADD COLUMN user_id INT DEFAULT NULL AFTER line_number"},
		{"ratings", "user_id", "ALTER TABLE ratings ADD COLUMN user_id INT DEFAULT NULL AFTER article_id"},
		{"ratings", "voter_key", "ALTER TABLE ratings ADD COLUMN voter_key VARCHAR(141) NOT NULL DEFAULT 'n:匿名' AFTER author_name"},
		{"webauthn_credentials", "name", "ALTER TABLE webauthn_credentials ADD COLUMN name VARCHAR(128) NOT NULL DEFAULT '未命名 Passkey' AFTER user_id"},
		// Per-user social links (email / GitHub / QQ). Moved out of the global
		// settings.yml `social` section so each user owns their own contact
		// info. Each column is NULL-tolerant; an empty profile renders no
		// social links at all.
		{"users", "social_email",  "ALTER TABLE users ADD COLUMN social_email  VARCHAR(255) DEFAULT NULL AFTER bio"},
		{"users", "social_github", "ALTER TABLE users ADD COLUMN social_github VARCHAR(255) DEFAULT NULL AFTER social_email"},
		{"users", "social_qq",     "ALTER TABLE users ADD COLUMN social_qq     VARCHAR(255) DEFAULT NULL AFTER social_github"},
		// backup_eligible: the go-webauthn lib compares the stored credential's
		// Flags.BackupEligible against the assertion's authenticator-data flag
		// on every login ("Backup Eligible flag inconsistency detected during
		// login validation"). We never persisted it, so the stored value was
		// always false, breaking login for any authenticator that sets BE=1.
		{"webauthn_credentials", "backup_eligible", "ALTER TABLE webauthn_credentials ADD COLUMN backup_eligible TINYINT(1) NOT NULL DEFAULT 0 AFTER transports"},
		// rendered_html stores the fully post-processed HTML for the public
		// (anonymous) view of every article — md/wikidot/bbcode/html/typst alike.
		// Pre-rendering at write time (and invalidating on every content/tag/
		// rating change) lets the GET handler serve HTML directly from a single
		// SELECT without re-parsing markdown/wikidot source. This is the core
		// of the "compile once, serve forever" performance story: the Go API
		// becomes a thin JSON assembler, and Next.js ISR + CDN cache the HTML
		// indefinitely until the webhook triggers a revalidate.
		{"articles", "rendered_html", "ALTER TABLE articles ADD COLUMN rendered_html MEDIUMTEXT DEFAULT NULL AFTER content"},
	}
	for _, m := range migrations {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM information_schema.COLUMNS
			 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			m.table, m.column).Scan(&n)
		if err != nil {
			slog.Warn("migration check failed", "table", m.table, "column", m.column, "err", err)
			continue
		}
		if n > 0 {
			continue // already applied
		}
		if _, err := db.Exec(m.alter); err != nil {
			slog.Warn("migration alter failed", "alter", m.alter, "err", err)
		}
	}
	// Index migrations can't be expressed in the table above because
	// information_schema.COLUMNS only knows about columns — for indexes
	// we'd need STATISTICS. The two indexes we add (idx_comment_user,
	// idx_rating_user) ride on the user_id columns above; MySQL creates
	// them in parallel CREATE TABLE entries for fresh installs. For
	// upgraded installs, add them once via `CREATE INDEX IF NOT EXISTS`
	// is not available in MySQL — we use the duplicate-index error
	// path instead, which is best-effort.
	addIndexes := []string{
		"ALTER TABLE comments ADD INDEX idx_comment_user (user_id)",
		"ALTER TABLE ratings ADD INDEX idx_rating_user (user_id)",
	}
	for _, s := range addIndexes {
		if _, err := db.Exec(s); err != nil {
			// 1061 = ER_DUP_KEYNAME. Tolerate it; log anything else.
			msg := err.Error()
			if !contains(msg, "Duplicate key name") && !contains(msg, "1061") {
				slog.Warn("index add failed", "stmt", s, "err", err)
			}
		}
	}
	return nil
}

// contains is a tiny substring helper to avoid dragging in strings just
// for the index-migration log filter.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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
		backup_eligible TINYINT(1) NOT NULL DEFAULT 0,
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

	// ═══ 15. typst_compile_queue — async compilation job queue ═══
	// Tracks the status of background typst compilation jobs. Articles are
	// saved immediately on publish and compilation happens asynchronously;
	// readers see a "compiling..." placeholder until the worker finishes.
	// The same article cannot have multiple pending jobs — enqueue uses
	// ON DUPLICATE KEY UPDATE to reset an existing failed/pending entry.
	`CREATE TABLE IF NOT EXISTS typst_compile_queue (
		id            INT AUTO_INCREMENT PRIMARY KEY,
		article_id    INT NOT NULL UNIQUE,
		status        ENUM('pending','compiling','success','failed') NOT NULL DEFAULT 'pending',
		error_message TEXT DEFAULT NULL,
		attempts      INT NOT NULL DEFAULT 0,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		compiled_at   DATETIME DEFAULT NULL,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		INDEX idx_status (status, created_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 16. article_deps — inter-article dependency tracking ═══
	// Records which articles include/import which other articles
	// (wikidot [[include]], typst @import). Used for cascading cache
	// invalidation: when article X changes, every article that depends
	// on X must have its rendered_html cleared and re-rendered.
	`CREATE TABLE IF NOT EXISTS article_deps (
		article_id    INT NOT NULL,
		depends_on_id INT NOT NULL,
		PRIMARY KEY (article_id, depends_on_id),
		INDEX idx_depends_on (depends_on_id),
		FOREIGN KEY (article_id)    REFERENCES articles(id) ON DELETE CASCADE,
		FOREIGN KEY (depends_on_id) REFERENCES articles(id) ON DELETE CASCADE
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
}
