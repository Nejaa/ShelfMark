package catalog

import (
	"context"
	"fmt"
	"net/url"
)

func (s *Service) openLibrary(ctx context.Context, query searchQuery) ([]Candidate, error) {
	params := url.Values{
		"q":      {queryText(query)},
		"limit":  {"15"},
		"fields": {"key,title,author_name,first_publish_year,language,isbn,publisher,cover_i,subject"},
	}
	if query.ISBN != "" {
		params.Set("q", "isbn:"+query.ISBN)
	} else if query.Title != "" {
		params.Del("q")
		params.Set("title", query.Title)
		if query.Author != "" {
			params.Set("author", query.Author)
		}
	}
	var data struct {
		Docs []struct {
			Key       string   `json:"key"`
			Title     string   `json:"title"`
			Authors   []string `json:"author_name"`
			Year      int      `json:"first_publish_year"`
			Language  []string `json:"language"`
			ISBN      []string `json:"isbn"`
			Publisher []string `json:"publisher"`
			Cover     int      `json:"cover_i"`
			Subjects  []string `json:"subject"`
		} `json:"docs"`
	}
	if err := s.getJSON(ctx, "https://openlibrary.org/search.json?"+params.Encode(), &data); err != nil {
		return nil, err
	}

	var out []Candidate
	for _, d := range data.Docs {
		c := Candidate{
			Title:      d.Title,
			Authors:    d.Authors,
			Source:     "Open Library",
			URL:        "https://openlibrary.org" + d.Key,
			CatalogURL: "https://openlibrary.org" + d.Key,
			Categories: d.Subjects,
			ISBNs:      d.ISBN, Languages: d.Language,
			WorkRecord:   true,
			SourceNotes:  "Work-level record; publication details may describe different editions.",
			FieldSources: fieldSources("Open Library"),
		}
		if d.Year > 0 {
			c.Pubdate = fmt.Sprint(d.Year)
		}

		if len(d.Publisher) > 0 {
			c.Publisher = d.Publisher[0]
		}

		if len(d.Language) > 0 {
			c.Language = d.Language[0]
		}

		if len(d.ISBN) > 0 {
			c.ISBN = d.ISBN[0]
			for _, isbn := range d.ISBN {
				if query.ISBN != "" && canonicalISBN(isbn) == canonicalISBN(query.ISBN) {
					c.ISBN = isbn
					break
				}
			}
		}

		if d.Cover > 0 {
			c.Cover = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-M.jpg", d.Cover)
		}

		out = append(out, c)
	}

	if query.ISBN != "" {
		var authors []string
		for _, candidate := range out {
			if canonicalISBN(candidate.ISBN) == canonicalISBN(query.ISBN) {
				authors = candidate.Authors
				break
			}
		}
		edition, err := s.openLibraryISBN(ctx, query.ISBN, authors)
		if err != nil {
			return out, err
		}
		if edition != nil {
			out = append(out, *edition)
		}
	}
	return out, nil
}
