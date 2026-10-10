package app

import (
	"errors"
	"log/slog"
	"net/http"

	"shelfmark/internal/library"
)

// discardDrafts validates the reviewed versions before clearing any patches.
// This prevents a stale browser tab from discarding newer edits.
func (s *Server) discardDrafts(w http.ResponseWriter, r *http.Request) {
	s.operations.Lock()
	defer s.operations.Unlock()

	var in struct {
		Books []struct {
			ID           string `json:"id"`
			DraftVersion string `json:"draft_version"`
		} `json:"books"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}
	if len(in.Books) == 0 {
		jsonError(w, errors.New("select at least one draft to discard"))
		return
	}

	staged, err := s.db.Staged(r.Context())
	if err != nil {
		jsonError(w, err)
		return
	}
	// Resolve IDs from persistent drafts, including those outside the scan.
	states := make(map[string]library.State, len(staged))
	for _, state := range staged {
		states[idFor(state.Path)] = state
	}

	selected := make([]string, 0, len(in.Books))
	out := make([]book, 0, len(in.Books))
	seen := make(map[string]bool, len(in.Books))
	for _, item := range in.Books {
		state, exists := states[item.ID]
		if seen[item.ID] || !exists || draftVersion(state.Staged) != item.DraftVersion {
			jsonError(w, errors.New("draft selection changed; reopen the review"))
			return
		}
		seen[item.ID] = true
		selected = append(selected, state.Path)
		out = append(out, makeBook(state.Path, state.Metadata, state.Metadata, false))
	}
	if err := s.db.DiscardDrafts(r.Context(), selected); err != nil {
		slog.Error("could not discard metadata drafts", "error", err)
		jsonError(w, err)
		return
	}
	for _, path := range selected {
		slog.Info("metadata draft discarded", "path", path)
	}
	jsonOut(w, map[string]any{"books": out})
}
