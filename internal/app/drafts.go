package app

import (
	"context"
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

// apply stages either a direct metadata patch or the current editor selection.
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

	snapshot, err := s.draftSnapshot(r.Context(), path)
	if err != nil {
		jsonError(w, err)
		return
	}

	staged, fileMeta := snapshot.staged, snapshot.fileMeta

	if in.Metadata == nil {
		in.Metadata, err = editorSelection(in.Values, in.Candidate, in.Fields, staged, filepath.Base(path))
		if err != nil {
			jsonError(w, err)
			return
		}
	}

	if err := metadata.ValidateFields(path, in.Metadata); err != nil {
		jsonError(w, err)
		return
	}

	for k, v := range in.Metadata {
		staged[k] = v
	}

	if err = s.db.Stage(r.Context(), path, snapshot.fingerprint, fileMeta, staged); err != nil {
		jsonError(w, err)
		return
	}

	slog.Info("metadata draft staged", "book_id", in.ID, "fields", len(in.Metadata))
	jsonOut(w, makeBook(path, staged, fileMeta, len(library.Changes(fileMeta, staged)) > 0))
}

// draftSnapshot captures the current file metadata and the staged working copy together.
type draftSnapshot struct {
	fileMeta, staged map[string]any
	fingerprint      string
}

// draftSnapshot reloads file metadata before reading draft state so stale reviews are rejected.
func (s *Server) draftSnapshot(ctx context.Context, path string) (draftSnapshot, error) {
	fileMeta, err := metadata.Read(path)
	if err != nil {
		return draftSnapshot{}, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return draftSnapshot{}, err
	}

	fp := fingerprint(path, info, fileMeta)
	staged, _, err := s.db.ReadBook(ctx, path, fp, fileMeta)
	if err != nil {
		return draftSnapshot{}, err
	}

	return draftSnapshot{fileMeta: fileMeta, staged: staged, fingerprint: fp}, nil
}

// editorSelection converts editor input into metadata. With a field list it
// restricts the result to those fields, layering edits over the chosen candidate.
func editorSelection(values map[string]string, candidate *catalog.Candidate, fields []string, staged map[string]any, filename string) (map[string]any, error) {
	edits, err := library.ParseInputs(values)
	if err != nil || fields == nil {
		return edits, err
	}

	proposed := library.Working(staged, nil)
	if candidate != nil {
		proposed = catalog.Proposal(staged, *candidate, filename)
	}

	proposed = library.Working(proposed, edits)
	selected := make(map[string]any, len(fields))
	for _, name := range fields {
		if _, known := library.Field(name); !known {
			return nil, fmt.Errorf("unknown metadata field %q", name)
		}
		selected[name] = proposed[name]
	}

	return selected, nil
}

// stagedCount reports how many books currently have pending draft changes.
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
		item, warning := s.reviewDraft(b)
		out = append(out, item)
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}

	jsonOut(w, map[string]any{"books": out, "warnings": warnings})
}

// reviewDraft rebuilds the current review row for one stored draft.
func (s *Server) reviewDraft(b library.State) (map[string]any, string) {
	info, err := os.Stat(b.Path)
	if err != nil {
		return s.unavailableDraftReview(b, "draft file unavailable during review", err)
	}

	fileMeta, err := metadata.Read(b.Path)
	if err != nil {
		return s.unavailableDraftReview(b, "draft metadata unreadable during review", err)
	}

	return s.availableDraftReview(b, info, fileMeta), ""
}

// unavailableDraftReview keeps a missing or unreadable draft visible to the user.
func (s *Server) unavailableDraftReview(b library.State, message string, err error) (map[string]any, string) {
	slog.Warn(message, "path", b.Path, "error", err)
	return unavailableDraft(b.Path, b.Metadata, b.Staged, err), fmt.Sprintf("%s: %v", b.Path, err)
}

// availableDraftReview rebuilds change details from the latest on-disk metadata.
func (s *Server) availableDraftReview(b library.State, info os.FileInfo, fileMeta map[string]any) map[string]any {
	draft := make(map[string]any, len(fileMeta)+len(b.Staged))
	for key, value := range fileMeta {
		draft[key] = value
	}
	for key, value := range b.Staged {
		draft[key] = value
	}

	cover, err := metadata.Cover(b.Path)
	if err != nil {
		slog.Warn("draft cover unavailable", "path", b.Path, "error", err)
	}

	changed := changedDraftFields(fileMeta, b.Staged)
	changes := draftReviewChanges(fileMeta, changed)

	return map[string]any{
		"id": idFor(b.Path), "path": b.Path, "name": filepath.Base(b.Path),
		"metadata": draft, "file_metadata": fileMeta, "changed": changed,
		"changes": changes, "cover": cover, "staged": true,
		"fingerprint": fingerprint(b.Path, info, fileMeta), "draft_version": draftVersion(b.Staged),
	}
}

// changedDraftFields keeps only fields whose staged values still differ from disk.
func changedDraftFields(fileMeta, staged map[string]any) map[string]any {
	changed := make(map[string]any)
	for key, value := range staged {
		if !metadata.IsEqual(fileMeta[key], value) {
			changed[key] = value
		}
	}

	return changed
}

// draftReviewChanges preserves the editor field order when listing staged changes.
func draftReviewChanges(fileMeta, changed map[string]any) []proposalField {
	changes := make([]proposalField, 0, len(changed))
	for _, name := range append(library.FieldNames(), "cover_url") {
		if value, ok := changed[name]; ok {
			changes = append(changes, proposalField{Name: name, Current: library.Text(fileMeta[name]), Value: library.Text(value), Changed: true})
		}
	}

	return changes
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

// saveBook is one reviewed draft selected for the save operation.
type saveBook struct {
	ID           string   `json:"id"`
	Fingerprint  string   `json:"fingerprint"`
	DraftVersion string   `json:"draft_version"`
	Fields       []string `json:"fields"`
}

// saveRequest is the batch payload for committing reviewed drafts.
type saveRequest struct {
	Books []saveBook `json:"books"`
}

// save preflights the whole selection, then saves books independently.
// Each write rechecks the snapshot because external programs can modify files
// even while the application holds its own operation lock.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	s.operations.Lock()
	defer s.operations.Unlock()

	var in saveRequest
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	stagedBooks, err := s.db.Staged(r.Context())
	if err != nil {
		jsonError(w, err)
		return
	}

	paths := draftPaths(stagedBooks)
	if err := s.validateSaveSelection(r.Context(), in.Books, paths); err != nil {
		jsonError(w, err)
		return
	}

	if err := uniqueSaveSelection(in.Books); err != nil {
		jsonError(w, err)
		return
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

// draftPaths indexes staged drafts by their opaque API identifiers.
func draftPaths(books []library.State) map[string]string {
	paths := make(map[string]string, len(books))
	for _, b := range books {
		paths[idFor(b.Path)] = b.Path
	}

	return paths
}

// validateSaveSelection checks every selected draft before the first write begins.
func (s *Server) validateSaveSelection(ctx context.Context, books []saveBook, paths map[string]string) error {
	// All preconditions are checked before the first file is written.
	for _, item := range books {
		path := paths[item.ID]
		if path == "" {
			return errors.New("draft no longer exists; reopen the review")
		}

		if err := s.validateSaveBook(ctx, path, item); err != nil {
			return err
		}
	}

	return nil
}

// validateSaveBook ensures the reviewed snapshot and selected fields are still valid.
func (s *Server) validateSaveBook(ctx context.Context, path string, item saveBook) error {
	state, err := s.db.State(ctx, path)
	if err != nil {
		return err
	}

	current, err := metadata.Read(path)
	if err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if item.Fingerprint != fingerprint(path, info, current) || item.DraftVersion != draftVersion(state.Staged) {
		return fmt.Errorf("%s changed since review; reopen the review", filepath.Base(path))
	}

	fields := make(map[string]any, len(item.Fields))
	for _, field := range item.Fields {
		fields[field] = state.Staged[field]
	}

	return metadata.ValidateFields(path, fields)
}

// uniqueSaveSelection rejects duplicate entries that would otherwise double-write a file.
func uniqueSaveSelection(books []saveBook) error {
	seen := make(map[string]bool, len(books))
	for _, item := range books {
		if seen[item.ID] {
			return errors.New("a book must appear only once per save")
		}

		seen[item.ID] = true
	}

	return nil
}

// draftVersion identifies the reviewed patch independently of the file snapshot.
func draftVersion(fields map[string]any) string {
	raw, _ := json.Marshal(fields)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
