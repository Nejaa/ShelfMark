package library

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeSearchText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "accents punctuation and case", in: "  L'Été — Tome 2! ", want: "l ete tome 2"},
		{name: "preserves meaningful digits", in: "Catch-22, Part 1.5", want: "catch 22 part 1 5"},
		{name: "empty input", in: " \t\n", want: ""},
		{name: "caps normalized input length", in: strings.Repeat("a", 520), want: strings.Repeat("a", 512)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeSearchText(tt.in); got != tt.want {
				t.Errorf("NormalizeSearchText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestClassifyBookLabel(t *testing.T) {
	tests := []struct {
		name, value, known string
		want               BookLabel
	}{
		{name: "explicit volume marker", value: "Discworld #3 - Equal Rites", want: BookLabel{Title: "Equal Rites", Series: "Discworld", Volume: "3"}},
		{name: "known series and bare volume", value: "The Expanse 2", known: "Expanse", want: BookLabel{Series: "Expanse", Volume: "2"}},
		{name: "known series label", value: "The Expanse", known: "Expanse", want: BookLabel{Series: "Expanse"}},
		{name: "unmarked number remains title", value: "Catch 22", known: "Discworld", want: BookLabel{Title: "Catch 22"}},
		{name: "ordinary title remains unchanged", value: "The Left Hand of Darkness", want: BookLabel{Title: "The Left Hand of Darkness"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyBookLabel(tt.value, tt.known); got != tt.want {
				t.Errorf("ClassifyBookLabel(%q, %q) = %#v, want %#v", tt.value, tt.known, got, tt.want)
			}
		})
	}
}

func TestFilenameBookLabel(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		identity SearchIdentity
		want     BookLabel
	}{
		{
			name:     "author and format excluded while separate volume is retained",
			filename: "Ursula Le Guin - Earthsea - 2,5 - Tehanu (epub).epub",
			identity: SearchIdentity{Authors: []string{"Ursula Le Guin"}},
			want:     BookLabel{Title: "Tehanu", Series: "Earthsea", Volume: "2.5"},
		},
		{
			name:     "release noise does not leave a title separator",
			filename: "Earthsea - 2 - Tehanu - [retail].epub",
			want:     BookLabel{Title: "Tehanu", Series: "Earthsea", Volume: "2"},
		},
		{
			name:     "known series marker label",
			filename: "Earthsea Tome 2 - The Tombs of Atuan.pdf",
			identity: SearchIdentity{Series: "Earthsea", Authors: []string{"Ursula K. Le Guin"}},
			want:     BookLabel{Title: "The Tombs of Atuan", Series: "Earthsea", Volume: "2"},
		},
		{
			name:     "year in title is not an invented volume",
			filename: "Catch 22 (1961).epub",
			want:     BookLabel{Title: "Catch 22 (1961)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FilenameBookLabel(tt.filename, tt.identity); got != tt.want {
				t.Errorf("FilenameBookLabel(%q, %#v) = %#v, want %#v", tt.filename, tt.identity, got, tt.want)
			}
		})
	}
}

func TestIdentifySearchUsesMetadataAndCleanFilename(t *testing.T) {
	tests := []struct {
		name     string
		values   map[string]any
		filename string
		want     SearchIdentity
	}{
		{
			name: "specific metadata precedes original and filename titles",
			values: map[string]any{
				"title": "Dune", "subtitle": "A Novel", "original_title": "Dune Messiah",
				"authors": []string{"Frank Herbert"}, "languages": []any{"en", "fr"},
			},
			filename: "Frank Herbert - Dune.epub",
			want:     SearchIdentity{Titles: []string{"Dune A Novel", "Dune Messiah", "Dune", "A Novel"}, Authors: []string{"Frank Herbert"}, Languages: []string{"en", "fr"}},
		},
		{
			name:     "placeholder title falls back to specific separated filename title",
			values:   map[string]any{"title": "Unknown", "authors": []string{"Ursula Le Guin"}, "series": "Earthsea"},
			filename: "Ursula Le Guin - Earthsea - Tehanu [epub].epub",
			want:     SearchIdentity{Titles: []string{"Tehanu"}, Authors: []string{"Ursula Le Guin"}, Series: "Earthsea"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IdentifySearch(tt.values, tt.filename); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IdentifySearch() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMetadataFieldParsingAndChanges(t *testing.T) {
	tests := []struct {
		name       string
		values     map[string]string
		current    map[string]any
		wantParsed map[string]any
		wantChange map[string]any
		wantErr    bool
	}{
		{
			name:       "list fields split on commas while scalar fields remain intact",
			values:     map[string]string{"authors": ` Ada Lovelace, "Martin Luther King, Jr." `, "title": "  Notes, from the Engine  ", "tags": "math, history"},
			current:    map[string]any{"title": "Notes, from the Engine", "authors": []any{"Ada Lovelace", "Martin Luther King, Jr."}, "tags": []string{"math"}, "unrelated": true},
			wantParsed: map[string]any{"authors": []string{"Ada Lovelace", "Martin Luther King, Jr."}, "title": "  Notes, from the Engine  ", "tags": []string{"math", "history"}},
			wantChange: map[string]any{"tags": []string{"math", "history"}},
		},
		{
			name:    "malformed quoted list value is rejected",
			values:  map[string]string{"authors": `Ada Lovelace, "unfinished`},
			wantErr: true,
		},
		{
			name:    "unknown field is rejected",
			values:  map[string]string{"private": "value"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseInputs(tt.values)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseInputs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(parsed, tt.wantParsed) {
				t.Errorf("ParseInputs() = %#v, want %#v", parsed, tt.wantParsed)
			}
			if got := Changes(tt.current, parsed); !reflect.DeepEqual(got, tt.wantChange) {
				t.Errorf("Changes() = %#v, want %#v", got, tt.wantChange)
			}
		})
	}
}

func TestInputTextRoundTripsCommaAndQuoteInContributorNames(t *testing.T) {
	want := []string{"Ada Lovelace", `Martin Luther King, Jr.`, `Alex "Lex" Smith`}
	encoded, err := InputText("authors", want)
	if err != nil {
		t.Fatalf("InputText() error = %v", err)
	}
	parsed, err := ParseInputs(map[string]string{"authors": encoded})
	if err != nil {
		t.Fatalf("ParseInputs() error = %v", err)
	}
	if !reflect.DeepEqual(parsed["authors"], want) {
		t.Fatalf("round-trip authors = %#v, want %#v (encoded %q)", parsed["authors"], want, encoded)
	}
}

func TestEqualTreatsUnsetAndEmptyRepresentationsAlike(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{name: "nil and empty list", a: nil, b: []string{}, want: true},
		{name: "blank scalar and nil", a: " \t", b: nil, want: true},
		{name: "JSON list representations", a: []string{"a", "b"}, b: []any{"a", "b"}, want: true},
		{name: "list order remains meaningful", a: []string{"a", "b"}, b: []string{"b", "a"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Equal(tt.a, tt.b); got != tt.want {
				t.Errorf("Equal(%#v, %#v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
