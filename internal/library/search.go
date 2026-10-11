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

var (
	separatorNormalizer = strings.NewReplacer(" \u2013 ", " - ", " \u2014 ", " - ")
	formatWords         = []string{"epub", "pdf", "cbz", "cbr", "mobi", "azw3", "retail", "ebook", "scan", "ocr"}
)

// SearchIdentity contains bibliographic clues, separate from editable metadata.
// Titles are ordered from the most useful local title to weaker alternatives.
type SearchIdentity struct {
	// Titles holds progressively weaker title candidates, starting with the best local clue.
	Titles []string

	// Authors contains contributor names that should accompany title-based searches.
	Authors []string

	// Series is the current series name, if any.
	Series string

	// SeriesIndex is the current series number, if any.
	SeriesIndex string

	// Languages keeps language hints that downstream search backends may use.
	Languages []string
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
	list := &titleList{seen: make(map[string]bool)}
	if generic {
		list.add(subtitle)
	} else {
		list.add(strings.TrimSpace(title + " " + subtitle))
	}
	list.add(original)

	if !generic {
		list.add(title)
		list.add(subtitle)
	}

	if generic || len(identity.Authors) == 0 {
		// Separated filenames often end with the actual title after author,
		// series and release markers. Keep both forms; do not guess an author.
		parts := strings.Split(separatorNormalizer.Replace(strings.ReplaceAll(filename, "_", " ")), " - ")
		if len(parts) > 1 {
			list.add(cleanFilename(parts[len(parts)-1], identity))
		}
		list.add(filenameTitle)
	}

	// A series title is a last resort, not a better clue than a specific subtitle.
	list.add(title)

	identity.Titles = list.titles

	return identity
}

// titleList keeps distinct, specific title candidates in insertion order.
type titleList struct {
	seen   map[string]bool
	titles []string
}

// add ignores duplicates and generic placeholders so weaker filename clues do not crowd out useful searches.
func (l *titleList) add(value string) {
	key := NormalizeSearchText(value)
	if key != "" && !l.seen[key] && !genericTitle(value) && !slices.Contains(formatWords, key) {
		l.seen[key] = true
		l.titles = append(l.titles, strings.TrimSpace(value))
	}
}

// genericTitle reports placeholders and other low-information titles that should not dominate search queries.
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

	parts := strings.Split(separatorNormalizer.Replace(filename), " - ")
	var titles []string
	for _, part := range parts {
		part = strings.Trim(part, " -_.[]()")
		key := NormalizeSearchText(part)
		if key == "" || key == NormalizeSearchText(identity.Series) || isKnownAuthor(key, identity.Authors) {
			continue
		}

		titles = append(titles, part)
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

// isKnownAuthor matches normalized filename parts against known author names without splitting person names into tokens.
func isKnownAuthor(key string, authors []string) bool {
	return slices.ContainsFunc(authors, func(author string) bool { return key == NormalizeSearchText(author) })
}
