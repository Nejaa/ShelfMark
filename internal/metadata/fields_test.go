package metadata

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFieldsEnforcesFormatCapabilities(t *testing.T) {
	for _, test := range []struct {
		name    string
		path    string
		fields  map[string]any
		wantErr string
	}{
		{name: "PDF publication date unsupported", path: "book.pdf", fields: map[string]any{"pubdate": "2020"}, wantErr: "publication date writing is not supported for PDF"},
		{name: "EPUB publication date supported", path: "book.epub", fields: map[string]any{"pubdate": "2020"}},
		{name: "CBZ year publication date supported", path: "book.cbz", fields: map[string]any{"pubdate": "2020"}},
		{name: "CBZ full ISO publication date supported", path: "book.cbz", fields: map[string]any{"pubdate": "2020-02-29"}},
		{name: "invalid CBZ date rejected", path: "book.cbz", fields: map[string]any{"pubdate": "2021-02-29"}, wantErr: "CBZ publication date"},
		{name: "CBZ unsupported ISBN rejected", path: "book.cbz", fields: map[string]any{"isbn": "9780306406157"}, wantErr: "isbn metadata is not supported for CBZ"},
		{name: "CBZ unsupported subtitle rejected", path: "book.cbz", fields: map[string]any{"subtitle": "A novel"}, wantErr: "subtitle metadata is not supported for CBZ"},
		{name: "CBZ language uses BCP 47 tag", path: "book.cbz", fields: map[string]any{"languages": []string{"zh-Hant"}}},
		{name: "CBZ language only allows one tag", path: "book.cbz", fields: map[string]any{"languages": []string{"en", "fr"}}, wantErr: "supports one language tag"},
		{name: "CBZ rating follows ComicInfo bounds", path: "book.cbz", fields: map[string]any{"rating": "4.5"}},
		{name: "CBZ rating over five rejected", path: "book.cbz", fields: map[string]any{"rating": "5.01"}, wantErr: "between 0 and 5"},
		{name: "cover only supported for EPUB", path: "book.cbz", fields: map[string]any{"cover_url": "https://example.org/cover.jpg"}, wantErr: "cover writing requires an EPUB"},
		{name: "unknown field rejected", path: "book.fb2", fields: map[string]any{"unknown": "x"}, wantErr: "unknown metadata field"},
		{name: "invalid list item rejected", path: "book.epub", fields: map[string]any{"authors": []any{"valid", 12}}, wantErr: "authors must contain strings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateFields(filepath.Clean(test.path), test.fields)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateFields() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateFields() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestISBNsExtractsValidDistinctValues(t *testing.T) {
	got := ISBNs("ISBN-10: 0-306-40615-2; ISBN-13: 978-0-306-40615-7; ISBN 0-306-40615-2; invalid 9780306406158")
	want := []string{"0306406152", "9780306406157"}
	if len(got) != len(want) {
		t.Fatalf("ISBNs() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ISBNs() = %#v, want %#v", got, want)
		}
	}
}
