package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
)

var translatorStatement = regexp.MustCompile(`(?i)(?:translated\s+(?:from.*?\s+)?by|traduite?\s+(?:de.*?\s+)?par|traduct(?:eur|rice)\s*:)\s+`)

// openLibraryISBN resolves edition-specific title, publisher and contributor
// information instead of presenting an aggregate work's arbitrary first ISBN.
// A missing edition is normal; transport failures remain visible to the caller.
func (s *Service) openLibraryISBN(ctx context.Context, isbn string, workAuthors []string) (*Candidate, error) {
	if canonicalISBN(isbn) == "" {
		return nil, nil
	}
	var data struct {
		Key         string   `json:"key"`
		Title       string   `json:"title"`
		Subtitle    string   `json:"subtitle"`
		Publishers  []string `json:"publishers"`
		PublishDate string   `json:"publish_date"`
		ISBN10      []string `json:"isbn_10"`
		ISBN13      []string `json:"isbn_13"`
		Series      []string `json:"series"`
		Covers      []int    `json:"covers"`
		ByStatement string   `json:"by_statement"`
		Languages   []struct {
			Key string `json:"key"`
		} `json:"languages"`
		Contributors []struct {
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"contributors"`
	}
	err := s.getJSON(ctx, "https://openlibrary.org/isbn/"+isbn+".json", &data)
	if err != nil {
		var response *catalogHTTPError
		if errors.As(err, &response) && response.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	if !strings.HasPrefix(data.Key, "/books/") || data.Title == "" {
		return nil, nil
	}
	candidate := Candidate{
		Title: data.Title, Subtitle: data.Subtitle, Authors: workAuthors,
		Publisher: first(data.Publishers), Pubdate: data.PublishDate, ISBN: isbn,
		ISBNs: append(data.ISBN13, data.ISBN10...), SeriesLabels: data.Series,
		Source: "Open Library", URL: "https://openlibrary.org" + data.Key,
		CatalogURL: "https://openlibrary.org" + data.Key, FieldSources: fieldSources("Open Library"),
		SourceNotes: "ISBN edition record; author names are supplied by the matching work when available.",
	}
	if !slices.ContainsFunc(candidate.ISBNs, func(value string) bool { return canonicalISBN(value) == canonicalISBN(isbn) }) {
		candidate.ISBN = first(candidate.ISBNs)
		candidate.SourceNotes += " Returned ISBNs do not confirm the requested identifier; verify the record."
	}
	for _, language := range data.Languages {
		candidate.Languages = append(candidate.Languages, path.Base(language.Key))
	}
	candidate.Language = first(candidate.Languages)
	for _, contributor := range data.Contributors {
		switch strings.ToLower(strings.TrimSpace(contributor.Role)) {
		case "trl", "translator", "translation", "traducteur", "traductrice":
			candidate.Translators = append(candidate.Translators, contributor.Name)
		}
	}
	if match := translatorStatement.FindStringIndex(data.ByStatement); match != nil {
		name, _, _ := strings.Cut(data.ByStatement[match[1]:], "]")
		name, _, _ = strings.Cut(name, ";")
		candidate.Translators = append(candidate.Translators, strings.TrimSpace(name))
	}
	if len(data.Covers) > 0 && data.Covers[0] > 0 {
		candidate.Cover = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", data.Covers[0])
	}
	return &candidate, nil
}
