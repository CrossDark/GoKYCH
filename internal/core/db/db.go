package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"gokych/internal/config"
)

// Init connects to MySQL, auto-creates the database if it doesn't exist,
// and returns a configured connection pool.
func Init(cfg config.Config) (*sql.DB, error) {
	mysql := cfg.MySQL

	// Step 1: connect without database to auto-create it.
	dsnNoDB := fmt.Sprintf("%s:%s@tcp(%s:%d)/?charset=%s&parseTime=true&timeout=10s",
		mysql.User, mysql.Password, mysql.Host, mysql.Port, mysql.Charset)
	dbNoDB, err := sql.Open("mysql", dsnNoDB)
	if err != nil {
		return nil, fmt.Errorf("open mysql (no db): %w", err)
	}
	defer dbNoDB.Close()

	createSQL := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		mysql.Database,
	)
	if _, err := dbNoDB.Exec(createSQL); err != nil {
		return nil, fmt.Errorf("create database %s: %w", mysql.Database, err)
	}
	log.Printf("[db] ensured database %q exists", mysql.Database)

	// Step 2: connect with database.
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=true&timeout=10s",
		mysql.User, mysql.Password, mysql.Host, mysql.Port, mysql.Database, mysql.Charset)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	// Pool settings.
	db.SetMaxOpenConns(mysql.Pool.MaxSize)
	db.SetMaxIdleConns(mysql.Pool.MinSize)
	db.SetConnMaxLifetime(time.Duration(mysql.Pool.PoolRecycle) * time.Second)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	log.Printf("[db] connected to %s:%d/%s (pool: min=%d, max=%d, recycle=%ds)",
		mysql.Host, mysql.Port, mysql.Database,
		mysql.Pool.MinSize, mysql.Pool.MaxSize, mysql.Pool.PoolRecycle)

	return db, nil
}
