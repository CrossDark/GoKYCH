package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-sql-driver/mysql"

	"gokych/internal/config"
)

// Init connects to MySQL, auto-creates the database if it doesn't exist,
// and returns a configured connection pool.
func Init(cfg config.Config) (*sql.DB, error) {
	mysqlCfg := cfg.MySQL

	// Step 1: connect without database to auto-create it.
	// Use mysql.Config to properly escape credentials and avoid DSN parsing issues.
	cfgNoDB := mysql.NewConfig()
	cfgNoDB.User = mysqlCfg.User
	cfgNoDB.Passwd = mysqlCfg.Password
	cfgNoDB.Net = "tcp"
	cfgNoDB.Addr = fmt.Sprintf("%s:%d", mysqlCfg.Host, mysqlCfg.Port)
	cfgNoDB.Params = map[string]string{
		"charset":      mysqlCfg.Charset,
		"parseTime":    "true",
		"timeout":      "10s",
		"readTimeout":  "30s",
		"writeTimeout": "30s",
	}
	dbNoDB, err := sql.Open("mysql", cfgNoDB.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql (no db): %w", err)
	}
	defer dbNoDB.Close()

	// Validate database name to prevent SQL injection (only allow alphanumeric and underscore)
	dbName := mysqlCfg.Database
	for _, r := range dbName {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return nil, fmt.Errorf("invalid database name: %s (only letters, numbers, and underscore allowed)", dbName)
		}
	}
	// Use backticks for identifier quoting (MySQL specific)
	createSQL := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		dbName,
	)
	if _, err := dbNoDB.Exec(createSQL); err != nil {
		return nil, fmt.Errorf("create database %s: %w", dbName, err)
	}
	slog.Info("database ensured", "name", dbName)

	// Step 2: connect with database.
	cfgWithDB := mysql.NewConfig()
	cfgWithDB.User = mysqlCfg.User
	cfgWithDB.Passwd = mysqlCfg.Password
	cfgWithDB.Net = "tcp"
	cfgWithDB.Addr = fmt.Sprintf("%s:%d", mysqlCfg.Host, mysqlCfg.Port)
	cfgWithDB.DBName = dbName
	cfgWithDB.Params = map[string]string{
		"charset":      mysqlCfg.Charset,
		"parseTime":    "true",
		"timeout":      "10s",
		"readTimeout":  "30s",
		"writeTimeout": "30s",
	}
	db, err := sql.Open("mysql", cfgWithDB.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	// Pool settings.
	// Note: SetMaxIdleConns sets the MAXIMUM number of idle connections, not minimum.
	// The config field name "MinSize" is misleading but kept for backward compatibility.
	db.SetMaxOpenConns(mysqlCfg.Pool.MaxSize)
	db.SetMaxIdleConns(mysqlCfg.Pool.MinSize)
	db.SetConnMaxLifetime(time.Duration(mysqlCfg.Pool.PoolRecycle) * time.Second)
	// Reclaim idle connections so closed-behind-NAT or DB-restarted conns are
	// dropped before a lazy query trips over them.
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	slog.Info("database connected",
		"host", mysqlCfg.Host,
		"port", mysqlCfg.Port,
		"database", dbName,
		"pool_max_idle", mysqlCfg.Pool.MinSize,
		"pool_max_open", mysqlCfg.Pool.MaxSize,
		"pool_recycle_s", mysqlCfg.Pool.PoolRecycle,
	)

	return db, nil
}
