package library

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	volumeMarker = regexp.MustCompile(`(?i)\b(?:t(?:ome)?|vol(?:ume)?\.?|book|livre|part|partie)\s*[-_. ]*\d+(?:[.,]\d+)?\b`)
	fileNoise    = regexp.MustCompile(`(?i)(?:\[(?:epub|pdf|cbz|cbr|mobi|azw3?|fb2|retail|ebook|scan|ocr|fr|en)]|\((?:epub|pdf|cbz|cbr|mobi|azw3?|fb2|retail|ebook|scan|ocr|fr|en)\)|\s+(?:epub|cbz|cbr|mobi|azw3?|fb2|retail|ebook|scan|ocr)$)`)
)

// SearchIdentity contains bibliographic clues, separate from editable metadata.
// Titles are ordered from the most useful local title to weaker alternatives.
type SearchIdentity struct {
	Titles      []string
	Authors     []string
	Series      string
	SeriesIndex string
	Languages   []string
}

// NormalizeSearchText folds case and accents and treats punctuation as word
// boundaries. It preserves numbers, which distinguish volumes and title parts.
// The length bound also caps the cost of edit-distance comparisons of bad input.
func NormalizeSearchText(value string) string {
	var out strings.Builder
	space := false
	count := 0
	for _, r := range norm.NFKD.String(value) {
		if unicode.IsMark(r) {
			continue
		}
		if count >= 512 {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			out.WriteRune(unicode.ToLower(r))
			space = false
			count++
		} else {
			space = true
		}
	}
	return out.String()
}

// IdentifySearch prefers metadata, but uses a cleaned filename when the title
// is missing, a placeholder, or merely the series and its volume number.
func IdentifySearch(values map[string]any, filename string) SearchIdentity {
	identity := SearchIdentity{
		Authors: Strings(values["authors"]), Series: strings.TrimSpace(Text(values["series"])),
		SeriesIndex: strings.TrimSpace(Text(values["series_index"])), Languages: Strings(values["languages"]),
	}
	title := strings.TrimSpace(Text(values["title"]))
	subtitle := strings.TrimSpace(Text(values["subtitle"]))
	original := strings.TrimSpace(Text(values["original_title"]))
	filenameTitle := cleanFilename(filename, identity)
	if filename != "" && NormalizeSearchText(title) == NormalizeSearchText(strings.TrimSuffix(filename, filepath.Ext(filename))) {
		title = filenameTitle
	}
	generic := genericTitle(title) || ClassifyBookLabel(title, identity.Series).Title == ""
	seen := make(map[string]bool)
	add := func(value string) {
		key := NormalizeSearchText(value)
		if key != "" && !seen[key] && !genericTitle(value) && !slices.Contains([]string{"epub", "pdf", "cbz", "cbr", "mobi", "azw3", "retail", "ebook", "scan", "ocr"}, key) {
			seen[key] = true
			identity.Titles = append(identity.Titles, strings.TrimSpace(value))
		}
	}
	if generic {
		add(subtitle)
	} else {
		add(strings.TrimSpace(title + " " + subtitle))
	}
	add(original)
	if !generic {
		add(title)
		add(subtitle)
	}
	if generic || len(identity.Authors) == 0 {
		// Separated filenames often end with the actual title after author,
		// series and release markers. Keep both forms; do not guess an author.
		parts := strings.Split(strings.NewReplacer(" – ", " - ", " — ", " - ").Replace(strings.ReplaceAll(filename, "_", " ")), " - ")
		if len(parts) > 1 {
			add(cleanFilename(parts[len(parts)-1], identity))
		}
		add(filenameTitle)
	}
	// A series title is a last resort, not a better clue than a specific subtitle.
	add(title)
	return identity
}

func genericTitle(title string) bool {
	switch NormalizeSearchText(title) {
	case "", "unknown", "unknown title", "untitled", "sans titre", "title", "titre", "book", "ebook":
		return true
	}
	return false
}

// cleanFilename removes explicit release/format and volume markers. Known
// authors and series can be removed from separated filename segments; arbitrary
// words, years and parenthetical title text are intentionally retained.
func cleanFilename(filename string, identity SearchIdentity) string {
	extension := filepath.Ext(filename)
	if slices.Contains([]string{".epub", ".pdf", ".cbz", ".cbr", ".mobi", ".azw", ".azw3", ".fb2"}, strings.ToLower(extension)) {
		filename = strings.TrimSuffix(filename, extension)
	}
	filename = filepath.Base(filename)
	filename = strings.ReplaceAll(filename, "_", " ")
	filename = volumeMarker.ReplaceAllString(filename, " ")
	filename = fileNoise.ReplaceAllString(filename, " ")
	parts := strings.Split(strings.NewReplacer(" – ", " - ", " — ", " - ").Replace(filename), " - ")
	var titles []string
	for _, part := range parts {
		part = strings.Trim(part, " -_.[]()")
		key := NormalizeSearchText(part)
		if key == "" || key == NormalizeSearchText(identity.Series) {
			continue
		}
		knownAuthor := false
		for _, author := range identity.Authors {
			if key == NormalizeSearchText(author) {
				knownAuthor = true
				break
			}
		}
		if !knownAuthor {
			titles = append(titles, part)
		}
	}
	filename = strings.Join(titles, " ")
	// A filename can prefix a series without separating it with a dash.
	if identity.Series != "" {
		prefix := identity.Series
		if len(filename) > len(prefix) && strings.EqualFold(filename[:len(prefix)], prefix) && unicode.IsSpace(rune(filename[len(prefix)])) {
			filename = filename[len(prefix):]
		}
	}
	return strings.Join(strings.Fields(strings.Trim(filename, " -_.[]()")), " ")
}
