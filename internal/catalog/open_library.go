package catalog

import (
	"context"
	"fmt"
	"net/url"
)

// openLibrary combines Open Library work search with optional ISBN edition lookup.
func (s *Service) openLibrary(ctx context.Context, query searchQuery) ([]Candidate, error) {
	params := openLibraryParams(query)
	var data openLibrarySearchResponse

	if err := s.getJSON(ctx, "https://openlibrary.org/search.json?"+params.Encode(), &data); err != nil {
		return nil, err
	}

	var out []Candidate

	for _, d := range data.Docs {
		out = append(out, openLibraryCandidate(d, query.ISBN))
	}

	if query.ISBN != "" {
		edition, err := s.openLibraryEdition(ctx, query.ISBN, out)
		if err != nil {
			return out, err
		}

		if edition != nil {
			out = append(out, *edition)
		}
	}

	return out, nil
}

// openLibrarySearchResponse mirrors the Open Library search payload fields ShelfMark uses.
type openLibrarySearchResponse struct {
	Docs []openLibrarySearchDoc `json:"docs"`
}

// openLibrarySearchDoc stores work-level search results returned by Open Library.
type openLibrarySearchDoc struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Authors   []string `json:"author_name"`
	Year      int      `json:"first_publish_year"`
	Language  []string `json:"language"`
	ISBN      []string `json:"isbn"`
	Publisher []string `json:"publisher"`
	Cover     int      `json:"cover_i"`
	Subjects  []string `json:"subject"`
}

// openLibraryParams prefers Open Library's dedicated title/author filters when possible.
func openLibraryParams(query searchQuery) url.Values {
	params := url.Values{
		"q":      {queryText(query)},
		"limit":  {"15"},
		"fields": {"key,title,author_name,first_publish_year,language,isbn,publisher,cover_i,subject"},
	}

	switch {
	case query.ISBN != "":
		params.Set("q", "isbn:"+query.ISBN)
	case query.Title != "":
		params.Del("q")
		params.Set("title", query.Title)
		if query.Author != "" {
			params.Set("author", query.Author)
		}
	}

	return params
}

// openLibraryCandidate normalizes an Open Library work record for ranking and review.
func openLibraryCandidate(doc openLibrarySearchDoc, queryISBN string) Candidate {
	candidate := Candidate{
		Title: doc.Title, Authors: doc.Authors,
		Source: "Open Library", URL: "https://openlibrary.org" + doc.Key,
		CatalogURL: "https://openlibrary.org" + doc.Key,
		Categories: doc.Subjects, ISBNs: doc.ISBN, Languages: doc.Language,
		WorkRecord: true, SourceNotes: "Work-level record; publication details may describe different editions.",
		FieldSources: fieldSources("Open Library"),
	}

	if doc.Year > 0 {
		candidate.Pubdate = fmt.Sprint(doc.Year)
	}

	if len(doc.Publisher) > 0 {
		candidate.Publisher = doc.Publisher[0]
	}

	if len(doc.Language) > 0 {
		candidate.Language = doc.Language[0]
	}

	if len(doc.ISBN) > 0 {
		candidate.ISBN = preferredOpenLibraryISBN(doc.ISBN, queryISBN)
	}

	if doc.Cover > 0 {
		candidate.Cover = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", doc.Cover)
	}

	return candidate
}

// preferredOpenLibraryISBN preserves the matching ISBN when the search started from one.
func preferredOpenLibraryISBN(isbns []string, queryISBN string) string {
	preferred := isbns[0]

	for _, isbn := range isbns {
		if queryISBN != "" && canonicalISBN(isbn) == canonicalISBN(queryISBN) {
			return isbn
		}
	}

	return preferred
}

// openLibraryEdition reuses authors from the matching work when resolving an edition by ISBN.
func (s *Service) openLibraryEdition(ctx context.Context, isbn string, candidates []Candidate) (*Candidate, error) {
	var authors []string

	for _, candidate := range candidates {
		if canonicalISBN(candidate.ISBN) == canonicalISBN(isbn) {
			authors = candidate.Authors
			break
		}
	}

	return s.openLibraryISBN(ctx, isbn, authors)
}
