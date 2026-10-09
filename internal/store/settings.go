package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting returns an empty value when a setting has never been saved.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	return value, err
}

// SetSetting updates a value and retires obsolete preferences atomically.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value); err != nil {
		return err
	}

	if key == "preferences" {
		// Retire legacy values atomically, including obsolete copies of the API key.
		if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key IN ('google_books_api_key','last_folder')"); err != nil {
			return err
		}
	}

	return tx.Commit()
}
