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

type priorityContextKey struct{}

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
	if request.Background {
		ctx = context.WithValue(ctx, priorityContextKey{}, true)
	} else {
		s.mu.Lock()
		s.manualWaiting++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.manualWaiting--
			s.mu.Unlock()
		}()
	}

	queries := planSearch(request)
	found := make(map[string]Candidate)
	failed := make(map[string]bool)
	strong := make(map[string]bool)
	exact := make(map[string]bool)
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
	slog.Debug("catalog search plan", "queries", queries, "custom_query", request.CustomQuery)

lookup:
	for _, q := range queries {
		for _, provider := range providers {
			if ctx.Err() != nil {
				break lookup
			}
			// Stop failed sources for this search, and broaden only sources whose
			// constrained queries did not find a convincing bibliographic match.
			if failed[provider.name] || exact[provider.name] || (q.Fallback && strong[provider.name]) {
				continue
			}
			started := time.Now()
			slog.Debug("catalog lookup started", "provider", provider.name, "query", q)
			candidates, err := provider.search(ctx, q)
			if err != nil {
				if ctx.Err() != nil {
					break lookup
				}
				// Transport errors may include an API key; getJSON logs safe details.
				slog.Warn("catalog lookup failed", "provider", provider.name, "query_kind", q.Kind, "hint", "check network access and catalog settings; try again later")
				result.Warnings = append(result.Warnings, provider.name+": lookup failed; try again later")
				failed[provider.name] = true
				if len(candidates) == 0 {
					continue
				}
			}
			slog.Debug("catalog lookup completed", "provider", provider.name, "query_kind", q.Kind, "candidates", len(candidates), "duration", time.Since(started))
			for _, candidate := range candidates {
				if strings.TrimSpace(candidate.Title) == "" || candidate.URL == "" {
					continue
				}
				candidate = normalizeContributors(candidate)
				rank := rankCandidate(request, candidate)
				candidate.Confidence = rank.Total
				strong[provider.name] = strong[provider.name] || (rank.Strong && specificCandidateTitle(request.Identity, candidate) != "")
				exact[provider.name] = exact[provider.name] || (rank.ISBN && specificCandidateTitle(request.Identity, candidate) != "")
				key := provider.name + "\x00" + candidate.URL
				if previous, ok := found[key]; !ok || candidate.Confidence > previous.Confidence {
					found[key] = candidate
				}
			}
		}
	}

	// Rank after collection so limits apply across all catalogs together.
	results := make([]Candidate, 0, len(found))
	for _, candidate := range found {
		rank := rankCandidate(request, candidate)
		slog.Debug("catalog candidate ranked", "source", candidate.Source, "title", candidate.Title, "score", rank.Total, "title_similarity", rank.Title, "author_similarity", rank.Author, "isbn_match", rank.ISBN)
		if rank.ISBN || (rank.Title >= .35 && candidate.Confidence >= .4) {
			results = append(results, candidate)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Confidence == results[j].Confidence {
			return results[i].URL < results[j].URL
		}
		return results[i].Confidence > results[j].Confidence
	})
	if prefs.MaxCandidates < 1 {
		prefs.MaxCandidates = 12
	}

	corroborateCandidates(request, results)
	if len(results) > prefs.MaxCandidates {
		results = results[:prefs.MaxCandidates]
	}

	result.Candidates = results
	return result
}

// CacheKey identifies a normalized query against a file and preference snapshot.
func CacheKey(fingerprint, query string) string {
	h := sha256.Sum256([]byte("matching-v3\x00" + fingerprint + "\x00" + strings.ToLower(strings.TrimSpace(query))))
	return hex.EncodeToString(h[:])
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

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
