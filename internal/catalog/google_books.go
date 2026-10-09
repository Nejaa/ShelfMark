package catalog

import (
	"context"
	"net/url"
	"strings"
)

func (s *Service) google(ctx context.Context, query searchQuery, key string) ([]Candidate, error) {
	q := queryText(query)
	if query.ISBN != "" {
		q = "isbn:" + query.ISBN
	} else if query.Title != "" {
		q = "intitle:" + quoteQuery(query.Title)
		if query.Author != "" {
			q += " inauthor:" + quoteQuery(query.Author)
		}
	}
	params := url.Values{"q": {q}, "maxResults": {"20"}}
	if key != "" {
		params.Set("key", key)
	}

	var data struct {
		Items []struct {
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
		} `json:"items"`
	}
	if err := s.getJSON(ctx, "https://www.googleapis.com/books/v1/volumes?"+params.Encode(), &data); err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(data.Items))
	for _, item := range data.Items {
		v := item.Volume
		c := Candidate{
			Title:        v.Title,
			Subtitle:     v.Subtitle,
			Authors:      v.Authors,
			Publisher:    v.Publisher,
			Pubdate:      v.PublishedDate,
			Description:  v.Description,
			Language:     v.Language,
			Categories:   v.Categories,
			URL:          "https://books.google.com/books?id=" + url.QueryEscape(item.ID),
			Source:       "Google Books",
			CatalogURL:   "https://books.google.com/books?id=" + url.QueryEscape(item.ID),
			FieldSources: map[string]string{},
		}
		for _, id := range v.Industry {
			if id.Type == "ISBN_13" || id.Type == "ISBN_10" {
				c.ISBNs = append(c.ISBNs, id.Identifier)
				if c.ISBN == "" || id.Type == "ISBN_13" {
					c.ISBN = id.Identifier
				}
			}
		}

		if u := v.ImageLinks["thumbnail"]; u != "" {
			c.Cover = strings.Replace(u, "http://", "https://", 1)
		}

		c.FieldSources = fieldSources(c.Source)
		out = append(out, c)
	}

	return out, nil
}
