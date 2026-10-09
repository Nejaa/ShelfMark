package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CacheGet decodes an unexpired entry; a nonpositive age disables the cache.
func (s *Store) CacheGet(ctx context.Context, key string, maxAge time.Duration, dest any) (bool, error) {
	if maxAge <= 0 {
		return false, nil
	}

	var value string
	err := s.db.QueryRowContext(ctx, `SELECT result_json FROM search_matches WHERE cache_key=? AND updated_at>=?`, key, time.Now().Add(-maxAge).Unix()).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if err = json.Unmarshal([]byte(value), dest); err != nil {
		return false, err
	}

	return true, nil
}

// CachePut stores a result and prunes the cache to 500 entries in one transaction.
func (s *Store) CachePut(ctx context.Context, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin catalog cache update: %w", err)
	}

	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO search_matches (cache_key, result_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			result_json = excluded.result_json,
			updated_at = excluded.updated_at
	`, key, string(encoded), time.Now().Unix()); err != nil {
		return fmt.Errorf("store catalog cache entry: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM search_matches WHERE cache_key IN (
			SELECT cache_key FROM search_matches
			ORDER BY updated_at DESC LIMIT -1 OFFSET 500
		)
	`); err != nil {
		return fmt.Errorf("prune catalog cache: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit catalog cache update: %w", err)
	}

	return nil
}
