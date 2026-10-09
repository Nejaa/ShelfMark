package catalog

import (
	"strings"

	"shelfmark/internal/library"
)

// SearchRequest separates local identity from user-entered search terms. Custom
// terms are respected; automatic searches can explore translated title variants.
type SearchRequest struct {
	Query       string
	CustomQuery bool
	Identity    library.SearchIdentity
	ISBNs       []string
	Background  bool
}

// searchQuery describes intent; each adapter translates it to its own syntax.
// Fallback queries loosen constraints only when that source has no strong match.
type searchQuery struct {
	Kind     string
	Title    string
	Author   string
	ISBN     string
	Text     string
	Fallback bool
}

func planSearch(request SearchRequest) []searchQuery {
	queries := make([]searchQuery, 0, 8)
	seen := make(map[string]bool)
	add := func(query searchQuery) {
		key := query.Kind + "\x00" + library.NormalizeSearchText(query.Title+" "+query.Author+" "+query.ISBN+" "+query.Text)
		if seen[key] || len(queries) >= 8 {
			return
		}
		seen[key] = true
		queries = append(queries, query)
	}
	// ISBNs are independent queries. Combining an ISBN with a noisy title can
	// hide an otherwise exact hit. Bound content-derived ISBNs to avoid searching
	// an entire bibliography found by content inspection.
	for _, isbn := range request.ISBNs[:min(len(request.ISBNs), 2)] {
		add(searchQuery{Kind: "isbn", ISBN: isbn})
	}
	if request.CustomQuery {
		add(searchQuery{Kind: "text", Text: strings.TrimSpace(request.Query)})
		return queries
	}
	titles := request.Identity.Titles
	if len(titles) > 3 {
		titles = titles[:3]
	}
	author := first(request.Identity.Authors)
	for _, title := range titles {
		add(searchQuery{Kind: "title-author", Title: title, Author: author})
	}
	if len(titles) == 0 {
		add(searchQuery{Kind: "text", Text: request.Query})
		return queries
	}
	for _, title := range titles {
		if author != "" {
			add(searchQuery{Kind: "title", Title: title, Fallback: true})
		}
		// Plain text also handles providers with incomplete field indexing.
		add(searchQuery{Kind: "text", Text: strings.TrimSpace(title + " " + author), Fallback: true})
	}
	return queries
}

func queryText(query searchQuery) string {
	if query.ISBN != "" {
		return query.ISBN
	}
	if query.Text != "" {
		return query.Text
	}
	return strings.TrimSpace(query.Title + " " + query.Author)
}

// quoteQuery prevents local punctuation from becoming provider query operators.
func quoteQuery(value string) string {
	return `"` + strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < ' ' {
			return ' '
		}
		return r
	}, value)), " ") + `"`
}
