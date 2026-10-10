package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"shelfmark/internal/catalog"
	"shelfmark/internal/library"
	"shelfmark/internal/metadata"
)

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	s.operations.Lock()
	defer s.operations.Unlock()
	var in struct {
		ID        string             `json:"id"`
		Metadata  map[string]any     `json:"metadata"`
		Values    map[string]string  `json:"values"`
		Candidate *catalog.Candidate `json:"candidate"`
		Fields    []string           `json:"fields"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	if in.Metadata != nil && (in.Values != nil || in.Candidate != nil || in.Fields != nil) {
		jsonError(w, errors.New("provide metadata or an editor selection, not both"))
		return
	}

	path := s.lookupPath(in.ID)
	if path == "" {
		jsonError(w, errors.New("book is not in the current scan"))
		return
	}

	fileMeta, err := metadata.Read(path)
	if err != nil {
		jsonError(w, err)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		jsonError(w, err)
		return
	}

	staged, _, err := s.db.ReadBook(r.Context(), path, fingerprint(path, info, fileMeta), fileMeta)
	if err != nil {
		jsonError(w, err)
		return
	}

	if in.Metadata == nil {
		edits, err := library.ParseInputs(in.Values)
		if err != nil {
			jsonError(w, err)
			return
		}
		in.Metadata = edits
		if in.Fields != nil {
			proposed := library.Working(staged, nil)
			if in.Candidate != nil {
				proposed = catalog.Proposal(staged, *in.Candidate, filepath.Base(path))
			}
			proposed = library.Working(proposed, edits)
			in.Metadata = make(map[string]any, len(in.Fields))
			for _, name := range in.Fields {
				if _, known := library.Field(name); !known {
					jsonError(w, fmt.Errorf("unknown metadata field %q", name))
					return
				}
				in.Metadata[name] = proposed[name]
			}
		}
	}
	if err := metadata.ValidateFields(path, in.Metadata); err != nil {
		jsonError(w, err)
		return
	}

	for k, v := range in.Metadata {
		staged[k] = v
	}

	if err = s.db.Stage(r.Context(), path, fingerprint(path, info, fileMeta), fileMeta, staged); err != nil {
		jsonError(w, err)
		return
	}

	slog.Info("metadata draft staged", "book_id", in.ID, "fields", len(in.Metadata))
	jsonOut(w, makeBook(path, staged, fileMeta, len(library.Changes(fileMeta, staged)) > 0))
}

func (s *Server) stagedCount(w http.ResponseWriter, r *http.Request) {
	books, err := s.db.Staged(r.Context())
	if err != nil {
		jsonError(w, err)
		return
	}
	jsonOut(w, map[string]int{"count": len(books)})
}

// staged builds a fresh review against files on disk, overlaying stored edits.
// Missing or unreadable books are reported without hiding other valid drafts.
func (s *Server) staged(w http.ResponseWriter, r *http.Request) {
	s.operations.RLock()
	defer s.operations.RUnlock()
	books, err := s.db.Staged(r.Context())
	if err != nil {
		jsonError(w, err)
		return
	}

	out := make([]map[string]any, 0, len(books))
	warnings := make([]string, 0)
	for _, b := range books {
		info, err := os.Stat(b.Path)
		if err != nil {
			slog.Warn("draft file unavailable during review", "path", b.Path, "error", err)
			warnings = append(warnings, fmt.Sprintf("%s: %v", b.Path, err))
			out = append(out, unavailableDraft(b.Path, b.Metadata, b.Staged, err))
			continue
		}

		fileMeta, err := metadata.Read(b.Path)
		if err != nil {
			slog.Warn("draft metadata unreadable during review", "path", b.Path, "error", err)
			warnings = append(warnings, fmt.Sprintf("%s: %v", b.Path, err))
			out = append(out, unavailableDraft(b.Path, b.Metadata, b.Staged, err))
			continue
		}

		draft := make(map[string]any, len(fileMeta)+len(b.Staged))
		for key, value := range fileMeta {
			draft[key] = value
		}

		for key, value := range b.Staged {
			draft[key] = value
		}

		changed := map[string]any{}
		for k, v := range b.Staged {
			if !metadata.IsEqual(fileMeta[k], v) {
				changed[k] = v
			}
		}

		// Keep drafts visible even when their values already match the file.
		// They can still be explicitly discarded from the review.

		changes := make([]proposalField, 0, len(changed))
		for _, name := range append(library.FieldNames(), "cover_url") {
			if value, ok := changed[name]; ok {
				changes = append(changes, proposalField{Name: name, Current: library.Text(fileMeta[name]), Value: library.Text(value), Changed: true})
			}
		}

		cover, err := metadata.Cover(b.Path)
		if err != nil {
			slog.Warn("draft cover unavailable", "path", b.Path, "error", err)
		}
		out = append(out, map[string]any{
			"id":            idFor(b.Path),
			"path":          b.Path,
			"name":          filepath.Base(b.Path),
			"metadata":      draft,
			"file_metadata": fileMeta,
			"changed":       changed,
			"changes":       changes,
			"cover":         cover,
			"staged":        true,
			"fingerprint":   fingerprint(b.Path, info, fileMeta),
			"draft_version": draftVersion(b.Staged),
		})
	}

	jsonOut(w, map[string]any{"books": out, "warnings": warnings})
}

// unavailableDraft keeps a broken or missing file visible for draft cleanup.
// Its stored snapshot is only for display; saving still requires a readable file.
func unavailableDraft(path string, current, staged map[string]any, err error) map[string]any {
	return map[string]any{
		"id": idFor(path), "path": path, "name": filepath.Base(path),
		"metadata": library.Working(current, staged), "file_metadata": current,
		"draft_version": draftVersion(staged), "unavailable": err.Error(),
		"changes": []proposalField{},
	}
}

// save preflights the whole selection, then saves books independently.
// Each write rechecks the snapshot because external programs can modify files
// even while the application holds its own operation lock.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	s.operations.Lock()
	defer s.operations.Unlock()
	var in struct {
		Books []struct {
			ID           string   `json:"id"`
			Fingerprint  string   `json:"fingerprint"`
			DraftVersion string   `json:"draft_version"`
			Fields       []string `json:"fields"`
		} `json:"books"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	stagedBooks, err := s.db.Staged(r.Context())
	if err != nil {
		jsonError(w, err)
		return
	}

	paths := make(map[string]string, len(stagedBooks))
	for _, b := range stagedBooks {
		paths[idFor(b.Path)] = b.Path
	}
	// All preconditions are checked before the first file is written.
	for _, item := range in.Books {
		path := paths[item.ID]
		if path == "" {
			jsonError(w, errors.New("draft no longer exists; reopen the review"))
			return
		}

		state, err := s.db.State(r.Context(), path)
		if err != nil {
			jsonError(w, err)
			return
		}

		current, err := metadata.Read(path)
		if err != nil {
			jsonError(w, err)
			return
		}

		info, err := os.Stat(path)
		if err != nil {
			jsonError(w, err)
			return
		}

		if item.Fingerprint != fingerprint(path, info, current) || item.DraftVersion != draftVersion(state.Staged) {
			jsonError(w, fmt.Errorf("%s changed since review; reopen the review", filepath.Base(path)))
			return
		}

		fields := map[string]any{}
		for _, field := range item.Fields {
			fields[field] = state.Staged[field]
		}

		if err := metadata.ValidateFields(path, fields); err != nil {
			jsonError(w, err)
			return
		}
	}

	seen := map[string]bool{}
	for _, item := range in.Books {
		if seen[item.ID] {
			jsonError(w, errors.New("a book must appear only once per save"))
			return
		}
		seen[item.ID] = true
	}

	saved := make([]map[string]any, 0)
	failures := make([]map[string]string, 0)

	slog.Info("saving reviewed drafts", "books", len(in.Books))
	for _, item := range in.Books {
		result, err := s.saveDraft(r.Context(), paths[item.ID], item.Fields, item.Fingerprint, item.DraftVersion)
		if err != nil {
			slog.Error("could not save book draft", "path", paths[item.ID], "error", err)
			failures = append(failures, map[string]string{"path": paths[item.ID], "error": err.Error()})
			continue
		}
		saved = append(saved, result)
	}

	slog.Info("draft save completed", "saved", len(saved), "failed", len(failures))
	jsonOut(w, map[string]any{"books": saved, "errors": failures})
}

// draftVersion identifies the reviewed patch independently of the file snapshot.
func draftVersion(fields map[string]any) string {
	raw, _ := json.Marshal(fields)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
