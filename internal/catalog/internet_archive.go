package catalog

import (
	"context"
	"net/url"

	"shelfmark/internal/library"
)

func (s *Service) internetArchive(ctx context.Context, query searchQuery) ([]Candidate, error) {
	q := queryText(query)
	if query.ISBN != "" {
		q = "isbn:" + quoteQuery(query.ISBN)
	} else if query.Title != "" {
		q = "title:" + quoteQuery(query.Title)
		if query.Author != "" {
			q += " AND creator:" + quoteQuery(query.Author)
		}
	}
	params := url.Values{
		"q":      {q},
		"fl[]":   {"identifier,title,creator,year,language,description,isbn"},
		"rows":   {"12"},
		"output": {"json"},
	}
	var data struct {
		Response struct {
			Docs []struct {
				ID          string `json:"identifier"`
				Title       string `json:"title"`
				ISBN        any    `json:"isbn"`
				Creator     any    `json:"creator"`
				Year        string `json:"year"`
				Language    any    `json:"language"`
				Description any    `json:"description"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := s.getJSON(ctx, "https://archive.org/advancedsearch.php?"+params.Encode(), &data); err != nil {
		return nil, err
	}

	var out []Candidate
	for _, doc := range data.Response.Docs {
		if doc.ID == "" || doc.Title == "" {
			continue
		}
		c := Candidate{
			Title:   doc.Title,
			Authors: library.Strings(doc.Creator),
			ISBN:    first(library.Strings(doc.ISBN)), ISBNs: library.Strings(doc.ISBN),
			Languages:    library.Strings(doc.Language),
			Pubdate:      doc.Year,
			Language:     first(library.Strings(doc.Language)),
			Description:  library.Text(doc.Description),
			URL:          "https://archive.org/details/" + url.PathEscape(doc.ID),
			CatalogURL:   "https://archive.org/details/" + url.PathEscape(doc.ID),
			Source:       "Internet Archive",
			FieldSources: fieldSources("Internet Archive"),
		}
		out = append(out, c)
	}

	return out, nil
}
