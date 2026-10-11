package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Candidate is a normalized catalog record with provenance and a match score.
type Candidate struct {
	// Bibliographic fields normalized from provider responses.
	Title         string   `json:"title"`
	Authors       []string `json:"authors"`
	Translators   []string `json:"translators"`
	Publisher     string   `json:"publisher"`
	Pubdate       string   `json:"pubdate"`
	ISBN          string   `json:"isbn"`
	ISBNs         []string `json:"isbns"`
	Languages     []string `json:"languages"`
	Series        string   `json:"series"`
	SeriesLabels  []string `json:"series_labels"`
	SeriesIndex   string   `json:"series_index"`
	OriginalTitle string   `json:"original_title"`
	Subtitle      string   `json:"subtitle"`
	Description   string   `json:"description"`
	Language      string   `json:"language"`
	Categories    []string `json:"categories"`
	Cover         string   `json:"cover"`

	// Record provenance and ranking, kept separate from editable metadata.
	URL          string             `json:"url"`
	Source       string             `json:"source"`
	Confidence   float64            `json:"confidence"`
	WorkRecord   bool               `json:"work_record"`
	Evidence     []MetadataEvidence `json:"evidence"`
	SourceNotes  string             `json:"source_notes"`
	FieldSources map[string]string  `json:"field_sources"`

	LocalTitle    string `json:"local_title"`
	LocalSubtitle string `json:"local_subtitle"`
	Identifiers   string `json:"identifiers"`
	CatalogURL    string `json:"catalog_url"`
}

// MarshalJSON guarantees list-valued fields remain arrays even when a provider
// omits them. This also normalizes older cached records when they are served.
func (c Candidate) MarshalJSON() ([]byte, error) {
	type record Candidate
	if c.Translators == nil {
		c.Translators = []string{}
	}
	if c.Authors == nil {
		c.Authors = []string{}
	}
	if c.Categories == nil {
		c.Categories = []string{}
	}
	if c.ISBNs == nil {
		c.ISBNs = []string{}
	}
	if c.Languages == nil {
		c.Languages = []string{}
	}
	return json.Marshal(record(c))
}

// Service searches enabled catalogs and shares request pacing across searches.
type Service struct {
	http          *http.Client
	mu            sync.Mutex
	lastRequest   time.Time
	manualWaiting int
}

// priorityContextKey marks background searches so the throttle can yield to manual work.
type priorityContextKey struct{}

// namedProvider pairs user-facing provider names with search implementations.
type namedProvider struct {
	name   string
	search func(context.Context, searchQuery) ([]Candidate, error)
}

// Result combines useful candidates with recoverable catalog failures.
type Result struct {
	Candidates []Candidate `json:"candidates"`
	Warnings   []string    `json:"warnings"`
}

// Options is a snapshot of the sources and result limit for one search.
type Options struct {
	GoogleBooks       bool
	OpenLibrary       bool
	InternetArchive   bool
	GoogleBooksAPIKey string
	MaxCandidates     int
}

// New creates a catalog service with a bounded HTTP request timeout.
func New() *Service {
	return &Service{http: &http.Client{Timeout: 15 * time.Second}}
}

// Search gathers candidates for the query and discovered ISBNs, deduplicates
// catalog URLs, then ranks the results against the local book metadata.
// Manual searches take priority over background requests at the shared throttle.
func (s *Service) Search(ctx context.Context, request SearchRequest, prefs Options) Result {
	result := Result{Candidates: []Candidate{}, Warnings: []string{}}

	ctx, finish := s.beginSearch(ctx, request.Background)
	defer finish()

	queries := planSearch(request)
	slog.Debug("catalog search plan", "queries", queries, "custom_query", request.CustomQuery)

	providers := s.enabledProviders(prefs)

	states := make(map[string]providerSearchState, len(providers))
	found := make(map[string]Candidate)
	for _, q := range queries {
		if ctx.Err() != nil {
			break
		}

		for _, provider := range providers {
			if ctx.Err() != nil {
				break
			}

			state := states[provider.name]
			if shouldSkipProvider(q, state) {
				continue
			}

			s.lookupProvider(ctx, request, q, provider, &state, &result, found)
			states[provider.name] = state
		}
	}

	result.Candidates = finalizeCandidates(request, found, prefs.MaxCandidates)

	return result
}

// providerSearchState records when a provider has already produced enough evidence for this search.
type providerSearchState struct {
	failed bool
	strong bool
	exact  bool
}

// beginSearch tracks manual waiters so background searches can defer politely.
func (s *Service) beginSearch(ctx context.Context, background bool) (context.Context, func()) {
	if background {
		return context.WithValue(ctx, priorityContextKey{}, true), func() {
			// Background searches do not increment manualWaiting, so there is nothing to decrement.
		}
	}

	s.mu.Lock()
	s.manualWaiting++
	s.mu.Unlock()

	return ctx, func() {
		s.mu.Lock()
		s.manualWaiting--
		s.mu.Unlock()
	}
}

// enabledProviders snapshots the currently enabled catalogs for this request.
func (s *Service) enabledProviders(prefs Options) []namedProvider {
	var providers []namedProvider

	if prefs.GoogleBooks {
		providers = append(providers, namedProvider{"Google Books", func(ctx context.Context, query searchQuery) ([]Candidate, error) {
			return s.google(ctx, query, prefs.GoogleBooksAPIKey)
		}})
	}

	if prefs.OpenLibrary {
		providers = append(providers, namedProvider{"Open Library", s.openLibrary})
	}

	if prefs.InternetArchive {
		providers = append(providers, namedProvider{"Internet Archive", s.internetArchive})
	}

	return providers
}

// shouldSkipProvider stops broader retries after a provider failed or already found a strong hit.
func shouldSkipProvider(query searchQuery, state providerSearchState) bool {
	return state.failed || state.exact || query.Fallback && state.strong
}

// lookupProvider records provider warnings without aborting the whole catalog search.
func (s *Service) lookupProvider(ctx context.Context, request SearchRequest, query searchQuery, provider namedProvider, state *providerSearchState, result *Result, found map[string]Candidate) {
	started := time.Now()
	slog.Debug("catalog lookup started", "provider", provider.name, "query", query)

	candidates, err := provider.search(ctx, query)
	if err != nil {
		if ctx.Err() != nil || !s.recordLookupFailure(ctx, query, provider.name, len(candidates), state, result) {
			return
		}
	}

	slog.Debug("catalog lookup completed", "provider", provider.name, "query_kind", query.Kind, "candidates", len(candidates), "duration", time.Since(started))

	for _, candidate := range candidates {
		s.addCandidate(request, provider, candidate, state, found)
	}
}

// recordLookupFailure converts provider errors into warnings that can be shown in the UI.
func (s *Service) recordLookupFailure(ctx context.Context, query searchQuery, provider string, count int, state *providerSearchState, result *Result) bool {
	if ctx.Err() != nil {
		return false
	}

	// Transport errors may include an API key; getJSON logs safe details.
	slog.Warn("catalog lookup failed", "provider", provider, "query_kind", query.Kind, "hint", "check network access and catalog settings; try again later")
	result.Warnings = append(result.Warnings, provider+": lookup failed; try again later")
	state.failed = true

	return count > 0
}

// addCandidate keeps the best-scoring normalized record for each provider URL.
func (s *Service) addCandidate(request SearchRequest, provider namedProvider, candidate Candidate, state *providerSearchState, found map[string]Candidate) {
	if strings.TrimSpace(candidate.Title) == "" || candidate.URL == "" {
		return
	}

	candidate = normalizeContributors(candidate)
	rank := rankCandidate(request, candidate)
	candidate.Confidence = rank.Total
	hasSpecificTitle := specificCandidateTitle(request.Identity, candidate) != ""
	state.strong = state.strong || rank.Strong && hasSpecificTitle
	state.exact = state.exact || rank.ISBN && hasSpecificTitle
	key := provider.name + "\x00" + candidate.URL

	if previous, ok := found[key]; !ok || candidate.Confidence > previous.Confidence {
		found[key] = candidate
	}
}

// finalizeCandidates re-ranks, corroborates, sorts, and trims the provider results.
func finalizeCandidates(request SearchRequest, found map[string]Candidate, limit int) []Candidate {
	results := make([]Candidate, 0, len(found))

	for _, candidate := range found {
		rank := rankCandidate(request, candidate)
		slog.Debug("catalog candidate ranked", "source", candidate.Source, "title", candidate.Title, "score", rank.Total, "title_similarity", rank.Title, "author_similarity", rank.Author, "isbn_match", rank.ISBN)
		if rank.ISBN || rank.Title >= .35 && candidate.Confidence >= .4 {
			results = append(results, candidate)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Confidence == results[j].Confidence {
			return results[i].URL < results[j].URL
		}

		return results[i].Confidence > results[j].Confidence
	})

	if limit < 1 {
		limit = 12
	}

	corroborateCandidates(request, results)

	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

// CacheKey identifies a normalized query against a file and preference snapshot.
func CacheKey(fingerprint, query string) string {
	h := sha256.Sum256([]byte("matching-v3\x00" + fingerprint + "\x00" + strings.ToLower(strings.TrimSpace(query))))
	return hex.EncodeToString(h[:])
}

// first returns the first string in a slice or an empty string.
func first(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// fieldSources marks each editable metadata field with the catalog that supplied it.
func fieldSources(source string) map[string]string {
	fields := make(map[string]string)
	for _, field := range []string{
		"title",
		"authors",
		"publisher",
		"pubdate",
		"isbn",
		"languages",
		"tags",
		"comments",
		"catalog_url",
		"cover_url",
	} {
		fields[field] = source
	}

	return fields
}
