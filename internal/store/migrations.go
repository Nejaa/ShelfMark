package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"shelfmark/internal/library"
)

// migrateDrafts converts legacy full metadata drafts into field patches.
// Collect updates before writing: SQLite has only one connection, and the read
// cursor must be closed before updates are issued within this transaction.
func migrateDrafts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT path, metadata_json, staged_json FROM book_state WHERE staged_json IS NOT NULL")
	if err != nil {
		return err
	}

	type update struct {
		path  string
		draft any
	}
	var updates []update
	for rows.Next() {
		var path, currentJSON, draftJSON string
		if err := rows.Scan(&path, &currentJSON, &draftJSON); err != nil {
			_ = rows.Close()
			return err
		}

		var current, draft map[string]any
		if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
			_ = rows.Close()
			return err
		}

		if err := json.Unmarshal([]byte(draftJSON), &draft); err != nil {
			_ = rows.Close()
			return err
		}

		changes := library.Changes(current, draft)
		var raw any
		if len(changes) > 0 {
			encoded, err := json.Marshal(changes)
			if err != nil {
				_ = rows.Close()
				return err
			}
			raw = string(encoded)
		}

		updates = append(updates, update{path, raw})
	}

	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}

	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, "UPDATE book_state SET staged_json=? WHERE path=?", update.draft, update.path); err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx, "PRAGMA user_version = 2")
	return err
}
