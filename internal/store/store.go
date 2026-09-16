package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	SQL *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	for _, p := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
	} {
		if _, err := sqlDB.Exec(p); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0600); err != nil && !os.IsNotExist(err) {
		_ = sqlDB.Close()
		return nil, err
	}
	db := &DB{SQL: sqlDB}
	if err := db.Migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.SQL.Close()
}

func (db *DB) Migrate() error {
	if _, err := db.SQL.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var ver int
		if _, err := fmt.Sscanf(name, "%d_", &ver); err != nil {
			return fmt.Errorf("迁移文件名无效：%s", name)
		}
		var exists int
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, ver).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.SQL.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行 %s：%w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, ver, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func (db *DB) Ctx() context.Context {
	return context.Background()
}
