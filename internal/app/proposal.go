package app

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"

	"shelfmark/internal/catalog"
	"shelfmark/internal/library"
	"shelfmark/internal/metadata"
)

type proposalField struct {
	Name           string `json:"name"`
	Current        string `json:"current"`
	Value          string `json:"value"`
	Changed        bool   `json:"changed"`
	Suggested      bool   `json:"suggested"`
	Supported      bool   `json:"supported"`
	Evidence       string `json:"evidence"`
	ReviewRequired bool   `json:"review_required"`
}

// proposal returns an editor model, including authoritative difference and
// format-support decisions. Raw form values are normalized on the server.
func (s *Server) proposal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string             `json:"id"`
		Candidate *catalog.Candidate `json:"candidate"`
		Values    map[string]string  `json:"values"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}
	path := s.lookupPath(in.ID)
	if path == "" {
		jsonError(w, errors.New("book is not in the current scan"))
		return
	}
	s.operations.RLock()
	defer s.operations.RUnlock()
	state, err := s.db.State(r.Context(), path)
	if err != nil {
		jsonError(w, err)
		return
	}
	current := library.Working(state.Metadata, state.Staged)
	resolution := catalog.MetadataProposal{Values: library.Working(current, nil)}
	if in.Candidate != nil {
		resolution = catalog.Reconcile(current, *in.Candidate, filepath.Base(path))
	}
	slog.Debug("metadata proposal reconciled", "path", path, "evidence", resolution.Evidence, "review_fields", resolution.Review)
	proposed := resolution.Values
	edits, err := library.ParseInputs(in.Values)
	if err != nil {
		jsonError(w, err)
		return
	}
	proposed = library.Working(proposed, edits)
	fields := make([]proposalField, 0)
	for _, name := range library.FieldNames() {
		if proposed[name] == nil {
			list, _ := library.Field(name)
			if list {
				proposed[name] = []string{}
			} else {
				proposed[name] = ""
			}
		}
		changed := !library.Equal(current[name], proposed[name])
		supported := metadata.ValidateFields(path, map[string]any{name: proposed[name]}) == nil
		_, edited := in.Values[name]
		evidence := resolution.Evidence[name]
		review := resolution.Review[name] && !edited
		if edited {
			evidence = "Manual edit"
		}
		fields = append(fields, proposalField{Evidence: evidence, ReviewRequired: review, Name: name, Current: library.Text(current[name]), Value: library.Text(proposed[name]),
			Changed: changed, Supported: supported, Suggested: changed && supported && !review && (edited || library.Text(proposed[name]) != "" || (name == "subtitle" && evidence != ""))})
	}
	notes := ""
	if in.Candidate != nil {
		notes = in.Candidate.SourceNotes
	}
	jsonOut(w, map[string]any{"fields": fields, "notes": notes, "cover_editable": metadata.ValidateFields(path, map[string]any{"cover_url": "https://example.org/cover"}) == nil})
}
