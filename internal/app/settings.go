package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"shelfmark/internal/metadata"
	"shelfmark/internal/settings"
)

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.operations.Lock()
	defer s.operations.Unlock()
	prefs, err := settings.Load(r.Context(), s.db)
	if err != nil {
		jsonError(w, err)
		return
	}

	if r.Method == http.MethodPost {
		// A partial update preserves omitted fields, including the secret.
		var patch json.RawMessage
		if err := decode(r, &patch); err != nil {
			jsonError(w, err)
			return
		}

		if !strings.HasPrefix(strings.TrimSpace(string(patch)), "{") {
			jsonError(w, fmt.Errorf("settings must be an object"))
			return
		}

		decoder := json.NewDecoder(bytes.NewReader(patch))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&prefs); err != nil {
			jsonError(w, err)
			return
		}

		// Presence matters: an unrelated partial update must not clear a CLI
		// override. Selecting a level explicitly applies it even if it was saved.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(patch, &fields); err != nil {
			jsonError(w, err)
			return
		}

		if err := settings.Save(r.Context(), s.db, prefs); err != nil {
			jsonError(w, err)
			return
		}
		if _, changed := fields["log_level"]; changed && s.logLevel != nil {
			s.logLevel.Set(prefs.LogLevel)
		}
		slog.Info("settings updated", "saved_log_level", prefs.LogLevel.String(), "inspect_content", prefs.InspectContent, "ocr", prefs.OCR)
		if prefs.InspectContent && prefs.OCR {
			metadata.CheckOCR(r.Context())
		}
	}

	// Show the effective threshold, including a startup CLI override. The
	// persistent preference is changed only by a successful settings save.
	if s.logLevel != nil {
		prefs.LogLevel = s.logLevel.Level()
	}
	configured := prefs.GoogleBooksAPIKey != ""
	prefs.GoogleBooksAPIKey = ""
	jsonOut(w, struct {
		settings.Preferences
		KeyConfigured bool               `json:"google_books_api_key_configured"`
		OCR           metadata.OCRStatus `json:"ocr_status"`
	}{
		Preferences:   prefs,
		KeyConfigured: configured,
		OCR:           metadata.OCRAvailability(r.Context(), prefs.InspectContent && prefs.OCR),
	})
}
