package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"shelfmark/internal/catalog"
	"shelfmark/internal/library"
	"shelfmark/internal/metadata"
	"shelfmark/internal/settings"
)

type memoryRepository struct {
	mu       sync.Mutex
	settings map[string]string
	states   map[string]library.State
	cache    map[string]json.RawMessage
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{settings: map[string]string{}, states: map[string]library.State{}, cache: map[string]json.RawMessage{}}
}

func (m *memoryRepository) GetSetting(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[key], nil
}

func (m *memoryRepository) SetSetting(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}

func (m *memoryRepository) CacheGet(_ context.Context, key string, _ time.Duration, dst any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.cache[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dst)
}

func (m *memoryRepository) CachePut(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[key] = raw
	return nil
}

func (m *memoryRepository) ReadBook(_ context.Context, path, fingerprint string, metadata map[string]any) (map[string]any, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[path]
	if !ok || state.Fingerprint != fingerprint {
		state = library.State{Path: path, Fingerprint: fingerprint, Metadata: cloneMap(metadata), Staged: map[string]any{}}
		m.states[path] = state
	}
	return library.Working(state.Metadata, state.Staged), len(state.Staged) > 0, nil
}

func (m *memoryRepository) Stage(_ context.Context, path, fingerprint string, metadata, staged map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[path] = library.State{Path: path, Fingerprint: fingerprint, Metadata: cloneMap(metadata), Staged: cloneMap(staged)}
	return nil
}

func (m *memoryRepository) State(_ context.Context, path string) (library.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[path]
	if !ok {
		return library.State{Path: path, Metadata: map[string]any{}, Staged: map[string]any{}}, nil
	}
	state.Metadata = cloneMap(state.Metadata)
	state.Staged = cloneMap(state.Staged)
	return state, nil
}

func (m *memoryRepository) Staged(_ context.Context) ([]library.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]library.State, 0)
	for _, state := range m.states {
		if len(state.Staged) > 0 {
			state.Metadata = cloneMap(state.Metadata)
			state.Staged = cloneMap(state.Staged)
			result = append(result, state)
		}
	}
	return result, nil
}

func (m *memoryRepository) DiscardDrafts(_ context.Context, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, path := range paths {
		state := m.states[path]
		state.Staged = map[string]any{}
		m.states[path] = state
	}
	return nil
}

func (m *memoryRepository) FinishSave(_ context.Context, path, fingerprint string, metadata, staged map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[path] = library.State{Path: path, Fingerprint: fingerprint, Metadata: cloneMap(metadata), Staged: cloneMap(staged)}
	return nil
}

func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

type fakeCatalog struct {
	mu      sync.Mutex
	calls   int
	request catalog.SearchRequest
	result  catalog.Result
}

func (f *fakeCatalog) Search(_ context.Context, request catalog.SearchRequest, _ catalog.Options) catalog.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.request = request
	return f.result
}

func (f *fakeCatalog) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestServer(t *testing.T) (*Server, *memoryRepository, string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "book.epub")
	writeTestEPUB(t, path, "Old title")
	db := newMemoryRepository()
	s := New(db, nil)
	response := request(t, s.Handler(), http.MethodPost, "/api/scan", `{"root":`+mustJSON(t, root)+`}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", response.Code, response.Body.String())
	}
	return s, db, root, path
}

func writeTestEPUB(t *testing.T, path, title string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	mimetype, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(mimetype, "application/epub+zip"); err != nil {
		t.Fatal(err)
	}
	var escapedTitle strings.Builder
	if err := xml.EscapeText(&escapedTitle, []byte(title)); err != nil {
		t.Fatal(err)
	}
	entries := []struct{ name, data string }{
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="BookId"><metadata><dc:title>%s</dc:title><dc:identifier id="BookId" opf:scheme="ISBN">urn:isbn:9780306406157</dc:identifier><dc:language>en</dc:language><dc:creator opf:role="aut">Test Author</dc:creator></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>`, escapedTitle.String())},
	}
	for _, entry := range entries {
		writer, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	chapter, err := zw.Create("OEBPS/chapter.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(chapter, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>Test chapter.</p></body></html>`); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func request(t *testing.T, handler http.Handler, method, target, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestHandlerRejectsMalformedJSONAndCrossOriginRequests(t *testing.T) {
	db := newMemoryRepository()
	server := New(db, nil)
	handler := server.Handler()

	for _, test := range []struct {
		name, method, path, body, contentType string
		origin, fetchSite                     string
		status                                int
	}{
		{name: "unknown field", method: http.MethodPost, path: "/api/scan", body: `{"root":"unused","unexpected":true}`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "trailing json", method: http.MethodPost, path: "/api/scan", body: `{"root":"unused"} {}`, contentType: "application/json", status: http.StatusBadRequest},
		{name: "wrong content type", method: http.MethodPost, path: "/api/scan", body: `{}`, contentType: "text/plain", status: http.StatusUnsupportedMediaType},
		{name: "foreign origin", method: http.MethodPost, path: "/api/scan", body: `{}`, contentType: "application/json", origin: "https://evil.example", status: http.StatusForbidden},
		{name: "cross site fetch", method: http.MethodPost, path: "/api/scan", body: `{}`, contentType: "application/json", fetchSite: "cross-site", status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d (%s)", response.Code, test.status, response.Body.String())
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("Content-Security-Policy") == "" {
				t.Fatalf("security headers missing: %#v", response.Header())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("API response Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestApplyStageSaveSelectedFieldAndDiscard(t *testing.T) {
	server, db, _, path := newTestServer(t)
	id := idFor(path)
	proposal := request(t, server.Handler(), http.MethodPost, "/api/proposal", `{"id":`+mustJSON(t, id)+`,"values":{"title":"Manual proposal"}}`, "application/json")
	if proposal.Code != http.StatusOK {
		t.Fatalf("proposal failed: %d %s", proposal.Code, proposal.Body.String())
	}
	var proposalResult struct {
		Fields        []proposalField `json:"fields"`
		CoverEditable bool            `json:"cover_editable"`
	}
	if err := json.Unmarshal(proposal.Body.Bytes(), &proposalResult); err != nil {
		t.Fatal(err)
	}
	if len(proposalResult.Fields) == 0 || !proposalResult.CoverEditable {
		t.Fatalf("proposal response missing editor fields: %+v", proposalResult)
	}
	foundTitle := false
	for _, field := range proposalResult.Fields {
		if field.Name == "title" {
			foundTitle = field.Value == "Manual proposal" && field.Changed
		}
	}
	if !foundTitle {
		t.Fatalf("manual title edit not reflected in proposal: %+v", proposalResult.Fields)
	}
	apply := request(t, server.Handler(), http.MethodPost, "/api/apply", `{"id":`+mustJSON(t, id)+`,"metadata":{"title":"Reviewed title","subtitle":"Unselected subtitle"}}`, "application/json")
	if apply.Code != http.StatusOK {
		t.Fatalf("apply failed: %d %s", apply.Code, apply.Body.String())
	}
	if state, _ := db.State(context.Background(), path); state.Staged["title"] != "Reviewed title" || state.Staged["subtitle"] != "Unselected subtitle" {
		t.Fatalf("apply did not stage supplied metadata: %#v", state.Staged)
	}

	stageView := request(t, server.Handler(), http.MethodPost, "/api/staged", `{}`, "application/json")
	if stageView.Code != http.StatusOK {
		t.Fatalf("staged review failed: %s", stageView.Body.String())
	}
	var review struct {
		Books []struct {
			Fingerprint  string `json:"fingerprint"`
			DraftVersion string `json:"draft_version"`
		} `json:"books"`
	}
	if err := json.Unmarshal(stageView.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	if len(review.Books) != 1 {
		t.Fatalf("staged review books = %d", len(review.Books))
	}
	first := review.Books[0]
	saveBody := mustJSON(t, map[string]any{"books": []any{map[string]any{"id": id, "fingerprint": first.Fingerprint, "draft_version": first.DraftVersion, "fields": []string{"title"}}}})
	saved := request(t, server.Handler(), http.MethodPost, "/api/save", saveBody, "application/json")
	if saved.Code != http.StatusOK {
		t.Fatalf("save failed: %d %s", saved.Code, saved.Body.String())
	}
	fileMetadata, err := readFileMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileMetadata["title"] != "Reviewed title" {
		t.Fatalf("saved file title = %#v", fileMetadata["title"])
	}
	state, _ := db.State(context.Background(), path)
	if state.Staged["subtitle"] != "Unselected subtitle" || state.Staged["title"] != nil {
		t.Fatalf("save should retain only unselected fields, got %#v", state.Staged)
	}

	stageView = request(t, server.Handler(), http.MethodPost, "/api/staged", `{}`, "application/json")
	if err := json.Unmarshal(stageView.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	second := review.Books[0]
	saveBody = mustJSON(t, map[string]any{"books": []any{map[string]any{"id": id, "fingerprint": second.Fingerprint, "draft_version": second.DraftVersion, "fields": []string{"subtitle"}}}})
	saved = request(t, server.Handler(), http.MethodPost, "/api/save", saveBody, "application/json")
	if saved.Code != http.StatusOK {
		t.Fatalf("second save failed: %d %s", saved.Code, saved.Body.String())
	}
	state, _ = db.State(context.Background(), path)
	if len(state.Staged) != 0 {
		t.Fatalf("all selected fields should be cleared after save, got %#v", state.Staged)
	}

	apply = request(t, server.Handler(), http.MethodPost, "/api/apply", `{"id":`+mustJSON(t, id)+`,"metadata":{"title":"Discard me"}}`, "application/json")
	if apply.Code != http.StatusOK {
		t.Fatalf("second apply failed: %s", apply.Body.String())
	}
	state, _ = db.State(context.Background(), path)
	discardBody := mustJSON(t, map[string]any{"books": []any{map[string]string{"id": id, "draft_version": draftVersion(state.Staged)}}})
	discard := request(t, server.Handler(), http.MethodPost, "/api/drafts/discard", discardBody, "application/json")
	if discard.Code != http.StatusOK {
		t.Fatalf("discard failed: %d %s", discard.Code, discard.Body.String())
	}
	state, _ = db.State(context.Background(), path)
	if len(state.Staged) != 0 {
		t.Fatalf("discard left staged fields: %#v", state.Staged)
	}
}

func TestSaveRejectsStaleFileAndDraftVersionsWithoutWriting(t *testing.T) {
	server, db, _, path := newTestServer(t)
	id := idFor(path)
	response := request(t, server.Handler(), http.MethodPost, "/api/apply", `{"id":`+mustJSON(t, id)+`,"metadata":{"title":"Staged title"}}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("apply failed: %s", response.Body.String())
	}
	response = request(t, server.Handler(), http.MethodPost, "/api/staged", `{}`, "application/json")
	var review struct {
		Books []struct {
			Fingerprint  string `json:"fingerprint"`
			DraftVersion string `json:"draft_version"`
		} `json:"books"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	if len(review.Books) != 1 {
		t.Fatalf("review failed: %s", response.Body.String())
	}
	preconditions := review.Books[0]
	writeTestEPUB(t, path, "External title")
	body := mustJSON(t, map[string]any{"books": []any{map[string]any{"id": id, "fingerprint": preconditions.Fingerprint, "draft_version": preconditions.DraftVersion, "fields": []string{"title"}}}})
	response = request(t, server.Handler(), http.MethodPost, "/api/save", body, "application/json")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("stale file save status = %d, want 400: %s", response.Code, response.Body.String())
	}
	meta, err := readFileMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta["title"] != "External title" {
		t.Fatalf("stale save overwrote external update: %#v", meta["title"])
	}
	if state, _ := db.State(context.Background(), path); state.Staged["title"] != "Staged title" {
		t.Fatalf("stale save cleared draft: %#v", state.Staged)
	}
}

func TestSaveAndDiscardRejectStaleDraftVersion(t *testing.T) {
	server, db, _, path := newTestServer(t)
	id := idFor(path)
	response := request(t, server.Handler(), http.MethodPost, "/api/apply", `{"id":`+mustJSON(t, id)+`,"metadata":{"title":"First draft"}}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("apply failed: %s", response.Body.String())
	}
	response = request(t, server.Handler(), http.MethodPost, "/api/staged", `{}`, "application/json")
	var review struct {
		Books []struct {
			Fingerprint  string `json:"fingerprint"`
			DraftVersion string `json:"draft_version"`
		} `json:"books"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &review); err != nil || len(review.Books) != 1 {
		t.Fatalf("staged review invalid: %v %s", err, response.Body.String())
	}
	oldReview := review.Books[0]
	response = request(t, server.Handler(), http.MethodPost, "/api/apply", `{"id":`+mustJSON(t, id)+`,"metadata":{"title":"Updated draft"}}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("revised apply failed: %s", response.Body.String())
	}
	oldSelection := map[string]any{"books": []any{map[string]any{"id": id, "fingerprint": oldReview.Fingerprint, "draft_version": oldReview.DraftVersion, "fields": []string{"title"}}}}
	response = request(t, server.Handler(), http.MethodPost, "/api/save", mustJSON(t, oldSelection), "application/json")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("stale draft save status = %d, want 400: %s", response.Code, response.Body.String())
	}
	discard := map[string]any{"books": []any{map[string]string{"id": id, "draft_version": oldReview.DraftVersion}}}
	response = request(t, server.Handler(), http.MethodPost, "/api/drafts/discard", mustJSON(t, discard), "application/json")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("stale draft discard status = %d, want 400: %s", response.Code, response.Body.String())
	}
	state, _ := db.State(context.Background(), path)
	if state.Staged["title"] != "Updated draft" {
		t.Fatalf("stale selection changed newer draft: %#v", state.Staged)
	}
}

func TestSearchCacheAndBackgroundCompletion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "book.epub")
	writeTestEPUB(t, path, "Cacheable title")
	db := newMemoryRepository()
	prefs := settings.Defaults()
	prefs.BackgroundDelay = 1
	raw, _ := json.Marshal(prefs)
	db.settings["preferences"] = string(raw)
	catalogue := &fakeCatalog{result: catalog.Result{Candidates: []catalog.Candidate{{Title: "Cacheable title", Authors: []string{"Test Author"}, Source: "test", Confidence: .9}}}}
	server := New(db, catalogue)
	response := request(t, server.Handler(), http.MethodPost, "/api/scan", `{"root":`+mustJSON(t, root)+`}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("scan failed: %s", response.Body.String())
	}
	id := idFor(path)
	body := `{"id":` + mustJSON(t, id) + `,"query":"Cacheable title"}`
	response = request(t, server.Handler(), http.MethodPost, "/api/search", body, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("search failed: %d %s", response.Code, response.Body.String())
	}
	response = request(t, server.Handler(), http.MethodPost, "/api/search/cached", `{"id":`+mustJSON(t, id)+`}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("cached search failed: %d %s", response.Code, response.Body.String())
	}
	if catalogue.callCount() != 1 {
		t.Fatalf("cached selection triggered catalog request; calls = %d", catalogue.callCount())
	}

	response = request(t, server.Handler(), http.MethodPost, "/api/background/start", `{"folder":`+mustJSON(t, root)+`}`, "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("background start failed: %d %s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response = request(t, server.Handler(), http.MethodGet, "/api/background", "", "")
		var status matchProgress
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Status == "completed" {
			if status.Completed != 1 || status.Failures != 0 {
				t.Fatalf("unexpected background progress: %+v", status)
			}
			server.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.Close()
	t.Fatal("background matching did not complete in time")
}

func readFileMetadata(path string) (map[string]any, error) {
	return metadata.Read(path)
}
