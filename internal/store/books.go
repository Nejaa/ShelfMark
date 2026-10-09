package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"shelfmark/internal/library"
)

// A single upsert keeps the observed snapshot and staged patch consistent.
const upsertBookSQL = `
	INSERT INTO book_state (path, fingerprint, metadata_json, staged_json, updated_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		fingerprint = excluded.fingerprint,
		metadata_json = excluded.metadata_json,
		staged_json = excluded.staged_json,
		updated_at = excluded.updated_at
`

// ReadBook refreshes the observed file snapshot and overlays surviving draft
// changes. Edits already present in the file are removed from the stored patch.
func (s *Store) ReadBook(ctx context.Context, path, fingerprint string, current map[string]any) (map[string]any, bool, error) {
	var stagedRaw sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT staged_json FROM book_state WHERE path=?`, path).Scan(&stagedRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	staged := map[string]any{}
	if stagedRaw.Valid && stagedRaw.String != "" {
		if err = json.Unmarshal([]byte(stagedRaw.String), &staged); err != nil {
			return nil, false, err
		}
	}

	staged = library.Changes(current, staged)
	working := clone(current)
	for k, v := range staged {
		working[k] = v
	}

	err = s.persistBook(ctx, path, fingerprint, current, staged)
	return working, len(staged) > 0, err
}

// Stage stores only differences between the file and the working metadata.
func (s *Store) Stage(ctx context.Context, path, fingerprint string, fileMetadata, draft map[string]any) error {
	return s.persistBook(ctx, path, fingerprint, fileMetadata, library.Changes(fileMetadata, draft))
}

// State returns the stored snapshot and draft for one file.
func (s *Store) State(ctx context.Context, path string) (library.State, error) {
	var state library.State
	var metadataJSON string
	var draft sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT path, fingerprint, metadata_json, staged_json
		FROM book_state WHERE path = ?
	`, path).Scan(&state.Path, &state.Fingerprint, &metadataJSON, &draft)
	if err != nil {
		return state, err
	}

	if err = json.Unmarshal([]byte(metadataJSON), &state.Metadata); err != nil {
		return state, err
	}

	if draft.Valid && draft.String != "" {
		err = json.Unmarshal([]byte(draft.String), &state.Staged)
	}

	return state, err
}

// Staged returns all persisted drafts, including books outside the current scan.
func (s *Store) Staged(ctx context.Context) ([]library.State, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, fingerprint, metadata_json, staged_json
		FROM book_state
		WHERE staged_json IS NOT NULL
		ORDER BY path COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()
	var out []library.State
	for rows.Next() {
		var state library.State
		var metadataJSON string
		var draft sql.NullString
		if err = rows.Scan(&state.Path, &state.Fingerprint, &metadataJSON, &draft); err != nil {
			return nil, err
		}

		if err = json.Unmarshal([]byte(metadataJSON), &state.Metadata); err != nil {
			return nil, err
		}

		if draft.Valid && draft.String != "" {
			if err = json.Unmarshal([]byte(draft.String), &state.Staged); err != nil {
				return nil, err
			}
		}

		out = append(out, state)
	}

	return out, rows.Err()
}

// FinishSave reconciles a successfully written file with any unselected edits.
func (s *Store) FinishSave(ctx context.Context, path, fingerprint string, fileMetadata, remaining map[string]any) error {
	return s.persistBook(ctx, path, fingerprint, fileMetadata, remaining)
}

func (s *Store) persistBook(ctx context.Context, path, fingerprint string, current, changes map[string]any) error {
	metadataJSON, err := json.Marshal(current)
	if err != nil {
		return err
	}

	// SQL NULL means there is no draft; an empty JSON object would still appear
	// in the staged-books query and give an incorrect draft count.
	var stagedJSON any
	if len(changes) > 0 {
		encoded, err := json.Marshal(changes)
		if err != nil {
			return err
		}

		stagedJSON = string(encoded)
	}

	_, err = s.db.ExecContext(ctx, upsertBookSQL,
		path, fingerprint, string(metadataJSON), stagedJSON, time.Now().Unix())
	return err
}

func clone(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
