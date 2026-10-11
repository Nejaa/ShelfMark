package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"shelfmark/internal/library"
)

func TestNormalizeContributors(t *testing.T) {
	tests := []struct {
		name        string
		authors     []string
		translators []string
		wantAuthors []string
		wantTrans   []string
	}{
		{
			name:        "explicit translated role moves names and deduplicates normalized spellings",
			authors:     []string{"  Jane Doe (translator) ", "Jane Doe", "Émile Zola [translator]", "A. Writer (traductrice)"},
			translators: []string{"jane doe", " Existing Translator "},
			wantAuthors: []string{"Jane Doe"},
			wantTrans:   []string{"jane doe", "Existing Translator", "Émile Zola", "A. Writer"},
		},
		{
			name:        "prose in a name does not invent a role",
			authors:     []string{"Translated by Alex Author", "Mary Author"},
			wantAuthors: []string{"Translated by Alex Author", "Mary Author"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeContributors(Candidate{Authors: tt.authors, Translators: tt.translators})
			if !reflect.DeepEqual(got.Authors, tt.wantAuthors) {
				t.Errorf("authors = %#v, want %#v", got.Authors, tt.wantAuthors)
			}
			if !reflect.DeepEqual(got.Translators, tt.wantTrans) {
				t.Errorf("translators = %#v, want %#v", got.Translators, tt.wantTrans)
			}
		})
	}
}

func TestCanonicalISBN(t *testing.T) {
	tests := []struct {
		name, value, want string
	}{
		{name: "valid ISBN-10 converts to ISBN-13", value: "0-306-40615-2", want: "9780306406157"},
		{name: "valid ISBN-10 X check digit", value: "0-8044-2957-X", want: "9780804429573"},
		{name: "valid ISBN-13", value: "978-0-306-40615-7", want: "9780306406157"},
		{name: "valid 979 ISBN-13", value: "9791090636071", want: "9791090636071"},
		{name: "invalid ISBN-10 checksum", value: "0306406153", want: ""},
		{name: "invalid ISBN-13 checksum", value: "9780306406158", want: ""},
		{name: "X is only valid as the ISBN-10 check digit", value: "X306406152", want: ""},
		{name: "wrong length", value: "306406152", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canonicalISBN(tt.value); got != tt.want {
				t.Errorf("canonicalISBN(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRankCandidateUsesIndependentTitleAuthorAndISBNEvidence(t *testing.T) {
	request := SearchRequest{
		Identity: library.SearchIdentity{Titles: []string{"Dune"}, Authors: []string{"Frank Herbert"}, Languages: []string{"en"}},
		ISBNs:    []string{"0-306-40615-2"},
	}
	tests := []struct {
		name       string
		candidate  Candidate
		wantISBN   bool
		wantStrong bool
		minScore   float64
		maxScore   float64
	}{
		{
			name:      "exact title author language and ISBN",
			candidate: Candidate{Title: "Dune", Authors: []string{"Frank Herbert"}, Language: "eng", ISBN: "9780306406157"},
			wantISBN:  true, wantStrong: true, minScore: .99, maxScore: 1,
		},
		{
			name:       "title and author without ISBN remains strong",
			candidate:  Candidate{Title: "Dune", Authors: []string{"Frank Herbert"}, Language: "en"},
			wantStrong: true, minScore: .99, maxScore: 1,
		},
		{
			name:      "title match with unrelated author is discounted",
			candidate: Candidate{Title: "Dune", Authors: []string{"Someone Else"}, Language: "en"},
			maxScore:  .7,
		},
		{
			name:      "invalid ISBN does not count as a hit",
			candidate: Candidate{Title: "Dune", ISBN: "9780306406158"},
			maxScore:  .7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rankCandidate(request, tt.candidate)
			if got.ISBN != tt.wantISBN {
				t.Errorf("ISBN match = %v, want %v", got.ISBN, tt.wantISBN)
			}
			if got.Strong != tt.wantStrong {
				t.Errorf("Strong = %v, want %v (rank %#v)", got.Strong, tt.wantStrong, got)
			}
			if tt.minScore != 0 && got.Total < tt.minScore || tt.maxScore != 0 && got.Total > tt.maxScore {
				t.Errorf("score %v outside [%v, %v]", got.Total, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestRankCandidatePenalizesDifferentNumberedTitle(t *testing.T) {
	request := SearchRequest{Identity: library.SearchIdentity{Titles: []string{"Dune 1"}}}
	correct := rankCandidate(request, Candidate{Title: "Dune 1"})
	wrongVolume := rankCandidate(request, Candidate{Title: "Dune 2"})
	if !(correct.Total > wrongVolume.Total) {
		t.Errorf("same-volume score %v should exceed wrong-volume score %v", correct.Total, wrongVolume.Total)
	}
}

func TestPlanSearch(t *testing.T) {
	tests := []struct {
		name    string
		request SearchRequest
		want    []searchQuery
		maxLen  int
	}{
		{
			name: "automatic searches start with a bounded set of ISBNs and specific title-author queries",
			request: SearchRequest{
				Query: "noisy file name", ISBNs: []string{"isbn-one", "isbn-two", "isbn-three"},
				Identity: library.SearchIdentity{Titles: []string{"Title A", "Title B", "Title C", "Title D"}, Authors: []string{"Author Name"}},
			},
			want: []searchQuery{
				{Kind: "isbn", ISBN: "isbn-one"}, {Kind: "isbn", ISBN: "isbn-two"},
				{Kind: "title-author", Title: "Title A", Author: "Author Name"},
				{Kind: "title-author", Title: "Title B", Author: "Author Name"},
				{Kind: "title-author", Title: "Title C", Author: "Author Name"},
			},
			maxLen: 8,
		},
		{
			name:    "custom query is preserved and prevents automatic variants",
			request: SearchRequest{Query: "  edited query ", CustomQuery: true, ISBNs: []string{"0-306-40615-2"}, Identity: library.SearchIdentity{Titles: []string{"Ignored"}}},
			want:    []searchQuery{{Kind: "isbn", ISBN: "0-306-40615-2"}, {Kind: "text", Text: "edited query"}},
		},
		{
			name:    "no identity falls back to the supplied query",
			request: SearchRequest{Query: "some search terms"},
			want:    []searchQuery{{Kind: "text", Text: "some search terms"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planSearch(tt.request)
			if tt.maxLen == 0 && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("planSearch() = %#v, want %#v", got, tt.want)
				return
			}
			if tt.maxLen > 0 {
				if len(got) > tt.maxLen {
					t.Fatalf("plan has %d queries, want at most %d: %#v", len(got), tt.maxLen, got)
				}
				if !reflect.DeepEqual(got[:len(tt.want)], tt.want) {
					t.Errorf("leading queries = %#v, want %#v", got[:len(tt.want)], tt.want)
				}
			}
		})
	}
}

func TestReconcilePreservesSeriesAndSeparatesRemoteTitle(t *testing.T) {
	current := map[string]any{
		"title": "Discworld 3", "series": "Discworld", "series_index": "3",
		"authors": []string{"Terry Pratchett"},
	}
	candidate := Candidate{
		Title: "Discworld #3 - Equal Rites", Authors: []string{"Terry Pratchett"},
		Publisher: "Corgi", Pubdate: "1987", ISBN: "9780552131063",
		CatalogURL: "https://catalog.example/book/3", Source: "Example Catalog",
	}
	got := Reconcile(current, candidate, "Terry Pratchett - Discworld - 3 - Equal Rites.epub")
	for key, want := range map[string]any{
		"title": "Equal Rites", "series": "Discworld", "series_index": "3",
		"authors": []string{"Terry Pratchett"}, "publisher": "Corgi", "pubdate": "1987",
		"catalog_url": "https://catalog.example/book/3",
	} {
		if !reflect.DeepEqual(got.Values[key], want) {
			t.Errorf("Values[%q] = %#v, want %#v", key, got.Values[key], want)
		}
	}
	if got.Review["title"] {
		t.Errorf("title review should not be required when the local title is only a series label: %#v", got.Review)
	}
	if got.Evidence["title"] != "Example Catalog; book title separated from series/volume" {
		t.Errorf("title evidence = %q", got.Evidence["title"])
	}
}

func TestReconcilePreservesEditionFieldsAcrossLanguageConflict(t *testing.T) {
	current := map[string]any{
		"title": "Le Petit Prince", "authors": []string{"Antoine de Saint-Exupéry"},
		"publisher": "Local Publisher", "pubdate": "1943", "isbn": "local-isbn", "languages": []string{"fr"},
	}
	candidate := Candidate{
		Title: "The Little Prince", Authors: []string{"Antoine de Saint-Exupéry"}, Publisher: "Remote Publisher",
		Pubdate: "1945", ISBN: "remote-isbn", Language: "en", Source: "Work Catalog", WorkRecord: true,
	}
	got := Reconcile(current, candidate, "Le Petit Prince.epub")
	for _, field := range []string{"title", "publisher", "pubdate", "isbn", "languages"} {
		if !reflect.DeepEqual(got.Values[field], current[field]) {
			t.Errorf("Values[%q] = %#v, want existing value %#v", field, got.Values[field], current[field])
		}
	}
	if got.Evidence["publisher"] == "" || got.Evidence["languages"] == "" {
		t.Errorf("expected preservation evidence for conflicting edition data, got %#v", got.Evidence)
	}
	if got.Review["title"] {
		t.Errorf("local title in its existing language should be preserved without review, got %#v", got.Review)
	}
}

func TestReconcileTransfersOnlySupportedCorroboratedEvidence(t *testing.T) {
	current := map[string]any{"title": "Unknown"}
	candidate := Candidate{
		Title: "Unknown", Source: "Target Catalog",
		Evidence: []MetadataEvidence{
			{Field: "title", Value: "The Specific Title", Source: "Donor Catalog", URL: "https://donor.example/record"},
			{Field: "publisher", Value: "Unrelated Publisher", Source: "Donor Catalog", URL: "https://donor.example/record"},
		},
	}
	got := Reconcile(current, candidate, "untitled.epub")
	if got.Values["title"] != "The Specific Title" {
		t.Errorf("transferred title = %#v, want specific corroborated title", got.Values["title"])
	}
	if got.Evidence["title"] != "Corroborated by Donor Catalog: https://donor.example/record" || !got.Review["title"] {
		t.Errorf("title provenance/review = %q / %v", got.Evidence["title"], got.Review["title"])
	}
	if _, ok := got.Values["publisher"]; ok {
		t.Errorf("publisher must not be copied from cross-record evidence: %#v", got.Values)
	}
}

func TestCorroborateCandidatesRequiresSharedValidISBNAndCompatibleLanguage(t *testing.T) {
	tests := []struct {
		name         string
		isbn         string
		donorLang    string
		wantEvidence bool
	}{
		{name: "shared valid ISBN", isbn: "9780306406157", donorLang: "en", wantEvidence: true},
		{name: "invalid ISBN is not a link", isbn: "9780306406158", donorLang: "en"},
		{name: "different language is not a link", isbn: "9780306406157", donorLang: "fr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := SearchRequest{Identity: library.SearchIdentity{Titles: []string{"Wanted Book"}, Languages: []string{"en"}}}
			candidates := []Candidate{
				{Title: "Unknown", ISBN: tt.isbn, Source: "Target", URL: "https://target.example/book"},
				{Title: "Wanted Book", ISBN: tt.isbn, Language: tt.donorLang, Source: "Donor", URL: "https://donor.example/book"},
			}
			corroborateCandidates(request, candidates)
			gotEvidence := len(candidates[0].Evidence) == 1 && candidates[0].Evidence[0].Field == "title"
			if gotEvidence != tt.wantEvidence {
				t.Errorf("title evidence = %#v, want present %v", candidates[0].Evidence, tt.wantEvidence)
			}
		})
	}
}

func TestCandidateMarshalJSONKeepsOmittedListsAsArrays(t *testing.T) {
	encoded, err := json.Marshal(Candidate{Title: "A Book"})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"authors", "translators", "categories", "isbns", "languages"} {
		if got := string(value[field]); got != "[]" {
			t.Errorf("JSON field %q = %q, want []", field, got)
		}
	}
}

// rewriteTransport routes fixed production API URLs to a local httptest server.
type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (r rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	target, err := url.Parse(r.target)
	if err != nil {
		return nil, err
	}
	clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
	clone.Host = target.Host
	return r.base.RoundTrip(clone)
}

func newTestService(server *httptest.Server) *Service {
	service := New()
	service.http = &http.Client{Transport: rewriteTransport{target: server.URL, base: http.DefaultTransport}}
	return service
}

func allowImmediateRequest(service *Service) {
	service.mu.Lock()
	service.lastRequest = time.Now().Add(-2 * time.Second)
	service.mu.Unlock()
}

func TestCatalogProviderParsingWithHTTPFixtures(t *testing.T) {
	seen := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		if r.Header.Get("User-Agent") != "Shelfmark/1.0 (local ebook metadata helper)" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/books/v1/volumes":
			if got := r.URL.Query().Get("q"); got != `intitle:"The Hobbit" inauthor:"J. R. R. Tolkien"` {
				t.Errorf("Google Books q = %q", got)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"hobbit-1","volumeInfo":{"title":"The Hobbit","subtitle":"There and Back Again","authors":["J. R. R. Tolkien"],"publisher":"Allen & Unwin","publishedDate":"1937","industryIdentifiers":[{"type":"ISBN_10","identifier":"054792822X"},{"type":"ISBN_13","identifier":"9780547928227"}],"imageLinks":{"thumbnail":"http://images.example/cover"},"language":"en","categories":["Fantasy"]}}]}`))
		case "/advancedsearch.php":
			if got := r.URL.Query().Get("q"); got != `title:"The Hobbit" AND creator:"J. R. R. Tolkien"` {
				t.Errorf("Internet Archive q = %q", got)
			}
			_, _ = w.Write([]byte(`{"response":{"docs":[{"identifier":"the-hobbit","title":"The Hobbit","isbn":["9780547928227","054792822X"],"creator":["J. R. R. Tolkien"],"year":"1937","language":["eng"],"description":"A hobbit goes on an adventure."},{"identifier":"","title":"Invalid row"}]}}`))
		case "/search.json":
			if r.URL.Query().Get("title") != "The Hobbit" || r.URL.Query().Get("author") != "J. R. R. Tolkien" {
				t.Errorf("Open Library query parameters = %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"docs":[{"key":"/works/OL123W","title":"The Hobbit","author_name":["J. R. R. Tolkien"],"first_publish_year":1937,"language":["eng"],"isbn":["054792822X","9780547928227"],"publisher":["Allen & Unwin"],"cover_i":123,"subject":["Fantasy"]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := newTestService(server)
	query := searchQuery{Kind: "title-author", Title: "The Hobbit", Author: "J. R. R. Tolkien"}

	allowImmediateRequest(service)
	google, err := service.google(context.Background(), query, "")
	if err != nil {
		t.Fatalf("Google Books search: %v", err)
	}
	if len(google) != 1 {
		t.Fatalf("Google Books candidates = %d, want 1", len(google))
	}
	if google[0].ISBN != "9780547928227" || !reflect.DeepEqual(google[0].ISBNs, []string{"054792822X", "9780547928227"}) {
		t.Errorf("Google Books ISBN data = %q / %#v", google[0].ISBN, google[0].ISBNs)
	}
	if google[0].Cover != "https://images.example/cover" || google[0].FieldSources["title"] != "Google Books" {
		t.Errorf("Google Books cover/provenance = %q / %#v", google[0].Cover, google[0].FieldSources)
	}

	allowImmediateRequest(service)
	archive, err := service.internetArchive(context.Background(), query)
	if err != nil {
		t.Fatalf("Internet Archive search: %v", err)
	}
	if len(archive) != 1 {
		t.Fatalf("Internet Archive candidates = %d, want 1", len(archive))
	}
	if archive[0].Title != "The Hobbit" || !reflect.DeepEqual(archive[0].Authors, []string{"J. R. R. Tolkien"}) || archive[0].ISBN != "9780547928227" || archive[0].Pubdate != "1937" {
		t.Errorf("Internet Archive candidate = %#v", archive[0])
	}

	allowImmediateRequest(service)
	openLibrary, err := service.openLibrary(context.Background(), query)
	if err != nil {
		t.Fatalf("Open Library search: %v", err)
	}
	if len(openLibrary) != 1 {
		t.Fatalf("Open Library candidates = %d, want 1", len(openLibrary))
	}
	if !openLibrary[0].WorkRecord || openLibrary[0].ISBN != "054792822X" || openLibrary[0].Cover != "https://covers.openlibrary.org/b/id/123-M.jpg" || openLibrary[0].Pubdate != "1937" {
		t.Errorf("Open Library candidate = %#v", openLibrary[0])
	}
	for _, endpoint := range []string{"/books/v1/volumes", "/advancedsearch.php", "/search.json"} {
		if !seen[endpoint] {
			t.Errorf("fixture endpoint %q was not called", endpoint)
		}
	}
}

func TestOpenLibraryISBNEditionParsingWithHTTPFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/isbn/0306406152.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"/books/OL123M","title":"Edition Title","subtitle":"An Edition","publishers":["Publisher"],"publish_date":"2001","isbn_10":["0306406152"],"isbn_13":["9780306406157"],"series":["A Series"],"covers":[456],"by_statement":"Translated from the French by Jane Doe; illustrated by Sam Artist","languages":[{"key":"/languages/eng"}],"contributors":[{"name":"J. Smith","role":"trl"},{"name":"Editor Name","role":"editor"}]}`))
	}))
	defer server.Close()
	service := newTestService(server)
	allowImmediateRequest(service)
	got, err := service.openLibraryISBN(context.Background(), "0306406152", []string{"Author Name"})
	if err != nil {
		t.Fatalf("openLibraryISBN() error = %v", err)
	}
	if got == nil {
		t.Fatal("openLibraryISBN() returned no edition")
	}
	if got.Title != "Edition Title" || got.Publisher != "Publisher" || got.Pubdate != "2001" || got.Language != "eng" {
		t.Errorf("edition fields = %#v", got)
	}
	if !reflect.DeepEqual(got.Authors, []string{"Author Name"}) || !reflect.DeepEqual(got.Translators, []string{"J. Smith", "Jane Doe"}) {
		t.Errorf("edition contributors = authors %#v translators %#v", got.Authors, got.Translators)
	}
	if got.Cover != "https://covers.openlibrary.org/b/id/456-M.jpg" || !strings.Contains(got.URL, "/books/OL123M") {
		t.Errorf("edition cover/URL = %q / %q", got.Cover, got.URL)
	}
	if got.ISBN != "0306406152" || !reflect.DeepEqual(got.ISBNs, []string{"9780306406157", "0306406152"}) {
		t.Errorf("edition ISBN data = %q / %#v", got.ISBN, got.ISBNs)
	}
}
