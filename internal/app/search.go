package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"shelfmark/internal/catalog"
	"shelfmark/internal/library"
	"shelfmark/internal/metadata"
	"shelfmark/internal/settings"
)

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID          string  `json:"id"`
		Query       *string `json:"query"`
		Priority    string  `json:"priority"`
		IgnoreCache bool    `json:"ignore_cache"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	result, err := s.findMatches(r.Context(), in.ID, in.Query, lookupOptions{Background: in.Priority == "background", IgnoreCache: in.IgnoreCache})
	if err != nil {
		jsonError(w, err)
		return
	}
	jsonOut(w, result)
}

// lookupOptions controls scheduling and cache reuse for one search action.
type lookupOptions struct {
	Background  bool
	IgnoreCache bool
}

// findMatches is shared by explicit lookups and the server-owned background job.
// A nil query asks the backend to derive terms from the current working metadata.
func (s *Server) findMatches(ctx context.Context, id string, query *string, options lookupOptions) (catalog.Result, error) {
	background := options.Background
	if !background {
		s.manualSearches.Add(1)
		defer s.manualSearches.Add(-1)
	}
	if query != nil && strings.TrimSpace(*query) == "" {
		return catalog.Result{}, errors.New("enter a title, author or ISBN")
	}
	select {
	case s.searches <- struct{}{}:
		defer func() {
			<-s.searches
		}()
	case <-ctx.Done():
		return catalog.Result{}, ctx.Err()
	}

	path := s.lookupPath(id)
	if path == "" {
		return catalog.Result{}, errors.New("book is not in the current scan")
	}

	prefs, err := settings.Load(ctx, s.db)
	if err != nil {
		return catalog.Result{}, err
	}

	slog.Info("metadata search started", "book_id", id, "background", background, "ignore_cache", options.IgnoreCache)
	if !prefs.GoogleBooks && !prefs.OpenLibrary && !prefs.InternetArchive {
		slog.Warn("metadata search unavailable: no catalogs enabled", "hint", "enable at least one catalog in Settings")
		return catalog.Result{
			Candidates: []catalog.Candidate{},
			Warnings:   []string{"Enable a catalog in Settings to search for matches."},
		}, nil
	}

	current, fp, err := s.searchSnapshot(ctx, path)
	if err != nil {
		return catalog.Result{}, err
	}

	identity := library.IdentifySearch(current, filepath.Base(path))
	terms := library.SearchQuery(current, filepath.Base(path))
	if query != nil {
		terms = strings.TrimSpace(*query)
	}
	if terms == "" {
		return catalog.Result{}, errors.New("enter a title, author or ISBN")
	}

	catalogOptions := catalog.Options{
		GoogleBooks: prefs.GoogleBooks, OpenLibrary: prefs.OpenLibrary,
		InternetArchive: prefs.InternetArchive, GoogleBooksAPIKey: prefs.GoogleBooksAPIKey,
		MaxCandidates: prefs.MaxCandidates,
	}
	encoded, _ := json.Marshal(struct {
		Catalog             catalog.Options
		InspectContent, OCR bool
		Current             map[string]any
		Identity            library.SearchIdentity
		CustomQuery         bool
	}{catalogOptions, prefs.InspectContent, prefs.OCR, current, identity, query != nil})
	key := catalog.CacheKey(fp+string(encoded), terms)
	// Bypassing cache reads still allows a successful fresh search to replace
	// the old entry. Failures retain the existing cache and are never cached.
	if options.IgnoreCache {
		slog.Debug("search cache bypassed by request", "book_id", id)
	} else {
		var cached catalog.Result
		ok, err := s.db.CacheGet(ctx, key, time.Duration(prefs.CacheDays)*24*time.Hour, &cached)
		if err != nil {
			slog.Warn("search cache read failed; querying catalogs", "error", err)
		}
		if ok && err == nil {
			slog.Debug("search cache hit", "book_id", id, "candidates", len(cached.Candidates))
			// Empty results avoid repeated background work, but an explicit lookup
			// must retry content inspection and all enabled catalogs for new matches.
			if background || len(cached.Candidates) > 0 {
				slog.Info("metadata search completed", "book_id", id, "candidates", len(cached.Candidates), "cached", true)
				return cached, nil
			}
			slog.Info("cached search has no matches; retrying explicit lookup", "book_id", id)
		} else {
			slog.Debug("search cache miss or disabled", "book_id", id)
		}
	}
	// Indexed content is read locally. It is never submitted to a catalog service.
	clues := ""
	if prefs.InspectContent && query == nil {
		clues = s.contentClues(ctx, path, prefs.OCR)
	}

	var isbns []string
	seenISBN := make(map[string]bool)
	isbnInputs := []string{terms}
	if query == nil {
		isbnInputs = append([]string{library.Text(current["isbn"]), library.Text(current["identifiers"])}, terms, clues)
	}
	for _, value := range isbnInputs {
		for _, isbn := range metadata.ISBNs(value) {
			if !seenISBN[isbn] {
				seenISBN[isbn] = true
				isbns = append(isbns, isbn)
			}
		}
	}
	slog.Debug("book search identity", "book_id", id, "path", path, "titles", identity.Titles, "authors", identity.Authors, "isbns", isbns)
	results := s.catalog.Search(ctx, catalog.SearchRequest{
		Query: terms, CustomQuery: query != nil, Identity: identity, ISBNs: isbns, Background: background,
	}, catalogOptions)
	if len(results.Warnings) == 0 && ctx.Err() == nil && prefs.CacheDays > 0 {
		if err := s.db.CachePut(ctx, key, results); err != nil {
			slog.Warn("could not cache search results", "error", err)
		}
	}

	slog.Info("metadata search completed", "book_id", id, "candidates", len(results.Candidates), "warnings", len(results.Warnings))
	return results, nil
}

func (s *Server) searchSnapshot(ctx context.Context, path string) (map[string]any, string, error) {
	s.operations.RLock()
	defer s.operations.RUnlock()
	current, err := metadata.Read(path)
	if err != nil {
		return nil, "", err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}

	state, err := s.db.State(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return library.Working(current, library.Changes(current, state.Staged)), fingerprint(path, info, current), nil
}

func (s *Server) contentClues(ctx context.Context, path string, ocr bool) string {
	s.operations.RLock()
	defer s.operations.RUnlock()
	return metadata.ReadText(ctx, path, ocr)
}
