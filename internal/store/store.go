package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Store persists settings, search results, and book drafts in a local SQLite file.
type Store struct {
	db *sql.DB
}

const schemaVersion = 2

// Open creates the data directory, opens SQLite, and applies schema migrations.
// A single connection serializes database access; file operations are coordinated
// by the application separately from database transactions.
func Open(ctx context.Context, filename string) (*Store, error) {
	filename, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	// A Windows drive belongs in the URI path, not its authority:
	// file:///C:/... rather than file://C:/.... Keep URL escaping for names
	// containing spaces, question marks, or fragment delimiters.
	path := filepath.ToSlash(filename)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := os.Chmod(filename, 0600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("protect database: %w", err)
	}

	if err := s.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	slog.Info("database opened", "path", filename, "schema_version", schemaVersion)
	return s, nil
}

// Close releases the database connection after HTTP requests have drained.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initialize(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}

	defer func() { _ = tx.Rollback() }()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	slog.Debug("checking database schema", "current", version, "supported", schemaVersion)
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, schemaVersion)
	}

	if version < schemaVersion {
		slog.Info("migrating database schema", "from", version, "to", schemaVersion)
	}
	if version < 1 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
			CREATE TABLE IF NOT EXISTS search_matches(cache_key TEXT PRIMARY KEY,result_json TEXT NOT NULL,updated_at INTEGER NOT NULL);
			CREATE TABLE IF NOT EXISTS book_state(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,metadata_json TEXT NOT NULL,staged_json TEXT,updated_at INTEGER NOT NULL);
			PRAGMA user_version = 1;
		`); err != nil {
			return fmt.Errorf("apply schema migration 1: %w", err)
		}
	}

	if version < 2 {
		if err := migrateDrafts(ctx, tx); err != nil {
			return fmt.Errorf("apply schema migration 2: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}

	return nil
}
