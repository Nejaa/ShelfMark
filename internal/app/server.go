package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"shelfmark/internal/webui"
)

// Server owns the current scan and coordinates requests against local files.
// operations serializes scans and writes against readers; pathsMu protects the
// scan index separately so catalog network requests do not hold the file lock.
type Server struct {
	db       Repository
	catalog  CatalogSearcher
	lifetime context.Context

	// Share the handler's concurrency-safe threshold for live settings changes.
	logLevel *slog.LevelVar

	// Coordinate local file access and cap concurrent catalog searches.
	operations sync.RWMutex
	searches   chan struct{}

	// Replace the scan index atomically; lookups do not hold operations.
	pathsMu sync.RWMutex
	paths   map[string]string
	root    string

	jobsMu         sync.Mutex
	background     *matchJob
	jobSequence    uint64
	manualSearches atomic.Int32
	lastUIError    atomic.Int64
}

// book is the per-title API model shared by scan, draft, and search responses.
type book struct {
	ID            string         `json:"id"`
	Path          string         `json:"path"`
	Name          string         `json:"name"`
	Metadata      map[string]any `json:"metadata"`
	FileMetadata  map[string]any `json:"file_metadata"`
	SearchQuery   string         `json:"search_query"`
	CoverEditable bool           `json:"cover_editable"`
	Staged        bool           `json:"staged"`
}

// Option configures an application dependency before requests are served.
type Option func(*Server)

// WithContext ties server-owned work to the application's lifetime.
func WithContext(ctx context.Context) Option {
	return func(s *Server) { s.lifetime = ctx }
}

// WithLogLevel connects Settings to the logging handler's live severity filter.
func WithLogLevel(level *slog.LevelVar) Option {
	return func(s *Server) { s.logLevel = level }
}

// New wires persistence and catalog search into an application instance.
func New(db Repository, searcher CatalogSearcher, options ...Option) *Server {
	server := &Server{
		db:       db,
		lifetime: context.Background(),
		paths:    make(map[string]string),
		searches: make(chan struct{}, 2),
		catalog:  searcher,
	}

	for _, option := range options {
		option(server)
	}

	return server
}

// Handler serves the embedded SPA and JSON API with shared request limits.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /", http.FileServerFS(webui.Files))

	mux.HandleFunc("GET /api/licenses", s.licenseArchive)
	mux.HandleFunc("GET /api/webview2-license", s.webviewLicense)

	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("POST /api/settings", s.settings)

	mux.HandleFunc("POST /api/browse", s.browse)
	mux.HandleFunc("POST /api/scan", s.scan)
	mux.HandleFunc("POST /api/library", s.libraryView)
	mux.HandleFunc("POST /api/proposal", s.proposal)

	mux.HandleFunc("POST /api/background/start", s.startBackground)
	mux.HandleFunc("POST /api/background/cancel", s.cancelBackground)
	mux.HandleFunc("GET /api/background", s.backgroundStatus)

	mux.HandleFunc("POST /api/cover", s.cover)

	mux.HandleFunc("POST /api/search", s.search)
	mux.HandleFunc("POST /api/search/cached", s.cachedSearch)

	mux.HandleFunc("POST /api/ui-error", s.uiError)

	mux.HandleFunc("POST /api/apply", s.apply)
	mux.HandleFunc("POST /api/staged-count", s.stagedCount)
	mux.HandleFunc("POST /api/staged", s.staged)
	mux.HandleFunc("POST /api/save", s.save)
	mux.HandleFunc("POST /api/drafts/discard", s.discardDrafts)

	return withRequestLogging(withSecurityHeaders(http.MaxBytesHandler(mux, 2<<20)))
}

// withSecurityHeaders prevents other websites from invoking the local file API.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if status, message := apiRequestError(r); status != 0 {
				writeJSON(w, status, map[string]string{"error": message})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// apiRequestError enforces the API's same-origin and JSON-only request contract.
func apiRequestError(r *http.Request) (int, string) {
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
		return http.StatusForbidden, "cross-origin requests are not allowed"
	}

	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return http.StatusForbidden, "cross-site requests are not allowed"
	}

	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method == http.MethodPost && mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, "Content-Type must be application/json"
	}

	return 0, ""
}

// sameOrigin accepts only the loopback UI origin that served the page.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host == host && (u.Scheme == "http" || u.Scheme == "https")
}

// jsonOut writes a successful JSON response with the shared API encoding.
func jsonOut(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, v)
}

// writeJSON centralizes content type and status handling for all JSON responses.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// jsonError maps common local-file failures onto HTTP status codes for the SPA.
func jsonError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		slog.Debug("API operation canceled")
	} else {
		slog.Warn("API operation failed", "error", err)
	}

	status := http.StatusBadRequest
	var oversized *http.MaxBytesError
	switch {
	case errors.As(err, &oversized):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, os.ErrPermission):
		status = http.StatusForbidden
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}

	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// decode accepts exactly one JSON value and rejects unknown request fields.
func decode(r *http.Request, dst any) error {
	defer func() { _ = r.Body.Close() }()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}

	return nil
}
