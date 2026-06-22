package schema

import (
	"database/sql"
	"log"

	"golang.org/x/crypto/bcrypt"
)

// Init creates all tables (idempotent). Errors are logged but don't halt startup.
func Init(db *sql.DB) {
	for _, ddl := range allTables {
		if _, err := db.Exec(ddl); err != nil {
			log.Printf("[schema] warning: %v", err)
		}
	}
	log.Println("[schema] all tables initialized")
}

// SeedAdmin inserts the default admin user if the users table is empty.
// If the user exists as admin, promotes to owner.
func SeedAdmin(db *sql.DB, username, password string) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Printf("[schema] error: bcrypt hash failed: %v", err)
		return
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count)
	if err != nil {
		log.Printf("[schema] error: check admin: %v", err)
		return
	}

	if count == 0 {
		_, err = db.Exec(
			"INSERT INTO users (username, password_hash, nickname, role) VALUES (?, ?, ?, 'owner')",
			username, string(hash), username,
		)
		if err != nil {
			log.Printf("[schema] error: seed admin: %v", err)
			return
		}
		log.Printf("[schema] seeded admin user %q", username)
	} else {
		// Promote admin → owner if needed.
		res, err := db.Exec(
			"UPDATE users SET role = 'owner' WHERE username = ? AND role = 'admin'",
			username,
		)
		if err != nil {
			log.Printf("[schema] error: promote admin: %v", err)
			return
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			log.Printf("[schema] promoted existing admin %q to owner", username)
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
		author_name VARCHAR(128) NOT NULL DEFAULT '匿名',
		content     VARCHAR(500) NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		INDEX idx_article_line (article_id, line_number)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

	// ═══ 6. ratings ═══
	`CREATE TABLE IF NOT EXISTS ratings (
		id          INT AUTO_INCREMENT PRIMARY KEY,
		article_id  INT NOT NULL,
		author_name VARCHAR(128) NOT NULL,
		score       DECIMAL(4,2) NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		UNIQUE KEY uq_user_article (article_id, author_name),
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
		INDEX idx_author_name (author_name)
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
}
