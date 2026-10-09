package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"shelfmark/internal/library"
	"shelfmark/internal/metadata"
)

// saveDraft commits selected fields to a file, then reconciles persistent state.
// File replacement and SQLite cannot participate in one atomic transaction. If
// reconciliation fails, the draft stays available for inspection and retry.
func (s *Server) saveDraft(ctx context.Context, path string, fields []string, expected, version string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	slog.Debug("checking reviewed file and draft", "path", path, "selected_fields", fields)
	state, err := s.db.State(ctx, path)
	if err != nil {
		return nil, err
	}

	current, err := metadata.Read(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if fingerprint(path, info, current) != expected || draftVersion(state.Staged) != version {
		return nil, fmt.Errorf("file or draft changed since review; reopen the review")
	}

	// Split the reviewed patch: unselected fields remain staged for later.
	selected := make(map[string]bool, len(fields))
	for _, field := range fields {
		selected[field] = true
	}

	apply, remaining := map[string]any{}, map[string]any{}
	for key, value := range state.Staged {
		if selected[key] {
			apply[key] = value
		} else {
			remaining[key] = value
		}
	}

	// Read back what the format writer actually preserved or normalized.
	if len(apply) > 0 {
		slog.Debug("writing selected metadata fields", "path", path, "fields", len(apply))
		if err := metadata.Write(ctx, path, apply); err != nil {
			return nil, fmt.Errorf("write metadata: %w", err)
		}
		current, err = metadata.Read(path)
		if err != nil {
			return nil, fmt.Errorf("file was written but could not be read: %w", err)
		}
	}

	info, err = os.Stat(path)
	if err != nil {
		return nil, err
	}

	remaining = library.Changes(current, remaining)
	// Complete bookkeeping even if the browser disconnects after replacement.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.db.FinishSave(persistCtx, path, fingerprint(path, info, current), current, remaining); err != nil {
		return nil, fmt.Errorf("file was written but the draft could not be updated: %w", err)
	}

	slog.Info("book metadata saved", "path", path, "written_fields", len(apply), "remaining_fields", len(remaining))
	working := make(map[string]any, len(current)+len(remaining))
	for key, value := range current {
		working[key] = value
	}

	for key, value := range remaining {
		working[key] = value
	}

	return map[string]any{"path": path, "metadata": working, "file_metadata": current, "staged": len(remaining) > 0, "search_query": library.SearchQuery(working, filepath.Base(path))}, nil
}
