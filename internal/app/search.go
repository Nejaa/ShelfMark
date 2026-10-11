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

// search runs an explicit catalog lookup for the selected book.
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

// cachedSearch loads suggestions on selection without scheduling a catalog
// request or running content inspection. A miss leaves explicit search to the user.
func (s *Server) cachedSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	result, err := s.findMatches(r.Context(), in.ID, nil, lookupOptions{CacheOnly: true})
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
	CacheOnly   bool
}

// findMatches is shared by explicit lookups and the server-owned background job.
// A nil query asks the backend to derive terms from the current working metadata.
func (s *Server) findMatches(ctx context.Context, id string, query *string, options lookupOptions) (catalog.Result, error) {
	if !options.Background && !options.CacheOnly {
		s.manualSearches.Add(1)
		defer s.manualSearches.Add(-1)
	}

	if err := validateSearchQuery(query); err != nil {
		return catalog.Result{}, err
	}

	releaseSearchSlot, err := s.acquireSearchSlot(ctx, options.CacheOnly)
	if err != nil {
		return catalog.Result{}, err
	}

	defer releaseSearchSlot()

	path, prefs, err := s.searchPathAndPreferences(ctx, id)
	if err != nil {
		return catalog.Result{}, err
	}

	if !hasEnabledCatalog(prefs) {
		return noCatalogResult(options.CacheOnly)
	}

	target, err := s.searchTerms(ctx, path, query)
	if err != nil {
		return catalog.Result{}, err
	}

	target.id, target.path, target.customQuery = id, path, query != nil

	return s.searchCandidates(ctx, target, options, prefs)
}

// searchTarget is the resolved book snapshot and query used for one catalog search.
type searchTarget struct {
	id, path    string
	current     map[string]any
	fingerprint string
	identity    library.SearchIdentity
	terms       string
	customQuery bool
}

// validateSearchQuery rejects blank manual searches before any I/O or locking.
func validateSearchQuery(query *string) error {
	if query != nil && strings.TrimSpace(*query) == "" {
		return errors.New("enter a title, author or ISBN")
	}

	return nil
}

// acquireSearchSlot limits concurrent live catalog requests without delaying cache-only lookups.
func (s *Server) acquireSearchSlot(ctx context.Context, cacheOnly bool) (func(), error) {
	if cacheOnly {
		return noopRelease, nil
	}

	// Reading cached matches must not wait behind a background catalog request.
	select {
	case s.searches <- struct{}{}:
		return func() { <-s.searches }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// noopRelease is returned when no search slot was taken, so nothing needs releasing.
func noopRelease() {
	// Intentionally empty.
}

// searchPathAndPreferences loads the scan path and current search settings together.
func (s *Server) searchPathAndPreferences(ctx context.Context, id string) (string, settings.Preferences, error) {
	path := s.lookupPath(id)
	if path == "" {
		return "", settings.Preferences{}, errors.New("book is not in the current scan")
	}

	prefs, err := settings.Load(ctx, s.db)

	return path, prefs, err
}

// hasEnabledCatalog reports whether at least one remote source can answer a search.
func hasEnabledCatalog(prefs settings.Preferences) bool {
	return prefs.GoogleBooks || prefs.OpenLibrary || prefs.InternetArchive
}

// noCatalogResult keeps cached-selection requests quiet while guiding explicit searches.
func noCatalogResult(cacheOnly bool) (catalog.Result, error) {
	if cacheOnly {
		return catalog.Result{Candidates: []catalog.Candidate{}}, nil
	}

	slog.Warn("metadata search unavailable: no catalogs enabled", "hint", "enable at least one catalog in Settings")

	return catalog.Result{
		Candidates: []catalog.Candidate{},
		Warnings:   []string{"Enable a catalog in Settings to search for matches."},
	}, nil
}

// searchTerms snapshots the current working metadata and derives the query terms to use.
func (s *Server) searchTerms(ctx context.Context, path string, query *string) (searchTarget, error) {
	current, fp, err := s.searchSnapshot(ctx, path)
	if err != nil {
		return searchTarget{}, err
	}

	identity := library.IdentifySearch(current, filepath.Base(path))
	terms := library.SearchQuery(current, filepath.Base(path))

	if query != nil {
		terms = strings.TrimSpace(*query)
	}

	if terms == "" {
		return searchTarget{}, errors.New("enter a title, author or ISBN")
	}

	return searchTarget{current: current, fingerprint: fp, identity: identity, terms: terms}, nil
}

// searchCandidates resolves cache reuse, optional content inspection, and the remote lookup itself.
func (s *Server) searchCandidates(ctx context.Context, target searchTarget, options lookupOptions, prefs settings.Preferences) (catalog.Result, error) {
	id, path, current, identity, terms, customQuery := target.id, target.path, target.current, target.identity, target.terms, target.customQuery
	fp := target.fingerprint
	background := options.Background

	if options.CacheOnly {
		slog.Debug("loading cached matches for book selection", "book_id", id)
	} else {
		slog.Info("metadata search started", "book_id", id, "background", background, "ignore_cache", options.IgnoreCache)
	}

	catalogOptions := catalog.Options{
		GoogleBooks: prefs.GoogleBooks, OpenLibrary: prefs.OpenLibrary,
		InternetArchive: prefs.InternetArchive, GoogleBooksAPIKey: prefs.GoogleBooksAPIKey,
		MaxCandidates: prefs.MaxCandidates,
	}

	key := searchCacheKey(fp, terms, catalogOptions, prefs, current, identity, customQuery)
	cached, reused, err := s.cachedMatches(ctx, key, id, prefs, options)
	if err != nil {
		return catalog.Result{}, err
	}

	if reused {
		return cached, nil
	}

	if options.CacheOnly {
		return catalog.Result{Candidates: []catalog.Candidate{}}, nil
	}

	clues := ""
	if prefs.InspectContent && !customQuery {
		clues = s.contentClues(ctx, path, prefs.OCR)
	}

	isbns := searchISBNs(current, terms, clues, !customQuery)
	slog.Debug("book search identity", "book_id", id, "path", path, "titles", identity.Titles, "authors", identity.Authors, "isbns", isbns)

	results := s.catalog.Search(ctx, catalog.SearchRequest{
		Query: terms, CustomQuery: customQuery, Identity: identity, ISBNs: isbns, Background: background,
	}, catalogOptions)

	s.cacheSearchResults(ctx, key, id, results, prefs.CacheDays)
	slog.Info("metadata search completed", "book_id", id, "candidates", len(results.Candidates), "warnings", len(results.Warnings))

	return results, nil
}

// searchCacheKey includes the file fingerprint, active catalog settings, and
// the search identity inputs so cached matches are reused only for equivalent searches.
func searchCacheKey(fp, terms string, options catalog.Options, prefs settings.Preferences, current map[string]any, identity library.SearchIdentity, customQuery bool) string {
	encoded, _ := json.Marshal(struct {
		Catalog             catalog.Options
		InspectContent, OCR bool
		Current             map[string]any
		Identity            library.SearchIdentity
		CustomQuery         bool
	}{options, prefs.InspectContent, prefs.OCR, current, identity, customQuery})

	return catalog.CacheKey(fp+string(encoded), terms)
}

// cachedMatches decides when cached results are final and when an explicit retry is needed.
func (s *Server) cachedMatches(ctx context.Context, key, id string, prefs settings.Preferences, options lookupOptions) (catalog.Result, bool, error) {
	if options.IgnoreCache {
		slog.Debug("search cache bypassed by request", "book_id", id)
		return catalog.Result{}, false, nil
	}

	var cached catalog.Result
	ok, err := s.db.CacheGet(ctx, key, time.Duration(prefs.CacheDays)*24*time.Hour, &cached)
	if err != nil {
		if options.CacheOnly {
			slog.Warn("could not load cached catalog matches", "book_id", id, "error", err)
			return catalog.Result{}, false, err
		}
		slog.Warn("search cache read failed; querying catalogs", "error", err)
	}

	if !ok || err != nil {
		slog.Debug("search cache miss or disabled", "book_id", id)
		return catalog.Result{}, false, nil
	}

	slog.Debug("search cache hit", "book_id", id, "candidates", len(cached.Candidates))
	if options.CacheOnly || options.Background || len(cached.Candidates) > 0 {
		if !options.CacheOnly {
			slog.Info("metadata search completed", "book_id", id, "candidates", len(cached.Candidates), "cached", true)
		}
		return cached, true, nil
	}

	slog.Info("cached search has no matches; retrying explicit lookup", "book_id", id)
	return catalog.Result{}, false, nil
}

// searchISBNs merges explicit terms, stored identifiers, and optional content clues without duplicates.
func searchISBNs(current map[string]any, terms, clues string, identifyCurrent bool) []string {
	inputs := []string{terms}
	if identifyCurrent {
		inputs = append([]string{library.Text(current["isbn"]), library.Text(current["identifiers"])}, terms, clues)
	}

	seen := make(map[string]bool)
	var isbns []string
	for _, value := range inputs {
		for _, isbn := range metadata.ISBNs(value) {
			if !seen[isbn] {
				seen[isbn] = true
				isbns = append(isbns, isbn)
			}
		}
	}

	return isbns
}

// cacheSearchResults stores only reusable outcomes and skips canceled requests.
func (s *Server) cacheSearchResults(ctx context.Context, key, id string, results catalog.Result, cacheDays int) {
	if (len(results.Candidates) > 0 || len(results.Warnings) == 0) && ctx.Err() == nil && cacheDays > 0 {
		if err := s.db.CachePut(ctx, key, results); err != nil {
			slog.Warn("could not cache search results", "error", err)
		} else {
			slog.Debug("catalog results cached", "book_id", id, "candidates", len(results.Candidates), "warnings", len(results.Warnings))
		}
	}
}

// searchSnapshot rereads the file so external edits cannot make a cached draft stale.
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

// contentClues extracts searchable text under the same read lock used for metadata access.
func (s *Server) contentClues(ctx context.Context, path string, ocr bool) string {
	s.operations.RLock()
	defer s.operations.RUnlock()
	return metadata.ReadText(ctx, path, ocr)
}
