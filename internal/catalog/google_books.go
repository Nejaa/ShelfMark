package catalog

import (
	"context"
	"net/url"
	"strings"
)

// googleBooksResponse mirrors the subset of the Google Books API used for ranking.
type googleBooksResponse struct {
	Items []googleBooksItem `json:"items"`
}

// googleBooksItem carries the volume fields ShelfMark can surface to the user.
type googleBooksItem struct {
	ID     string `json:"id"`
	Volume struct {
		Title         string   `json:"title"`
		Subtitle      string   `json:"subtitle"`
		Authors       []string `json:"authors"`
		Publisher     string   `json:"publisher"`
		PublishedDate string   `json:"publishedDate"`
		Description   string   `json:"description"`
		Industry      []struct {
			Type       string `json:"type"`
			Identifier string `json:"identifier"`
		} `json:"industryIdentifiers"`
		ImageLinks map[string]string `json:"imageLinks"`
		Language   string            `json:"language"`
		Categories []string          `json:"categories"`
	} `json:"volumeInfo"`
}

// google searches Google Books with provider-specific query syntax.
func (s *Service) google(ctx context.Context, query searchQuery, key string) ([]Candidate, error) {
	params := googleBooksParams(query)

	if key != "" {
		params.Set("key", key)
	}

	var data googleBooksResponse

	if err := s.getJSON(ctx, "https://www.googleapis.com/books/v1/volumes?"+params.Encode(), &data); err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(data.Items))

	for _, item := range data.Items {
		out = append(out, googleBooksCandidate(item))
	}

	return out, nil
}

// googleBooksParams prefers ISBN and fielded title/author searches over plain text.
func googleBooksParams(query searchQuery) url.Values {
	q := queryText(query)

	switch {
	case query.ISBN != "":
		q = "isbn:" + query.ISBN
	case query.Title != "":
		q = "intitle:" + quoteQuery(query.Title)
		if query.Author != "" {
			q += " inauthor:" + quoteQuery(query.Author)
		}
	}

	return url.Values{"q": {q}, "maxResults": {"20"}}
}

// googleBooksCandidate normalizes a Google Books volume into ShelfMark fields.
func googleBooksCandidate(item googleBooksItem) Candidate {
	volume := item.Volume
	recordURL := "https://books.google.com/books?id=" + url.QueryEscape(item.ID)

	candidate := Candidate{
		Title: volume.Title, Subtitle: volume.Subtitle, Authors: volume.Authors,
		Publisher: volume.Publisher, Pubdate: volume.PublishedDate, Description: volume.Description,
		Language: volume.Language, Categories: volume.Categories,
		URL: recordURL, CatalogURL: recordURL, Source: "Google Books",
		ISBNs: googleBooksISBNs(volume.Industry),
	}

	candidate.ISBN = preferredGoogleBooksISBN(volume.Industry)

	if thumbnail := volume.ImageLinks["thumbnail"]; thumbnail != "" {
		candidate.Cover = strings.Replace(thumbnail, "http://", "https://", 1)
	}

	candidate.FieldSources = fieldSources(candidate.Source)

	return candidate
}

// googleBooksISBNs keeps only ISBN identifiers from Google's mixed identifier list.
func googleBooksISBNs(identifiers []struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}) []string {
	var isbns []string

	for _, identifier := range identifiers {
		if isGoogleBooksISBN(identifier.Type) {
			isbns = append(isbns, identifier.Identifier)
		}
	}

	return isbns
}

// preferredGoogleBooksISBN favors ISBN-13 while preserving the provider's original formatting.
func preferredGoogleBooksISBN(identifiers []struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}) string {
	var preferred string

	for _, identifier := range identifiers {
		if isGoogleBooksISBN(identifier.Type) && (preferred == "" || identifier.Type == "ISBN_13") {
			preferred = identifier.Identifier
		}
	}

	return preferred
}

// isGoogleBooksISBN reports whether Google labeled the identifier as an ISBN.
func isGoogleBooksISBN(kind string) bool {
	return kind == "ISBN_13" || kind == "ISBN_10"
}
