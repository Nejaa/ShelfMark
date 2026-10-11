package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	numberedLabel     = regexp.MustCompile(`(?i)^(.+?)\s+(?:[-:]\s*)?(?:t(?:ome)?|vol(?:ume)?\.?|book|livre|#)\s*([0-9]+(?:[.,][0-9]+)?)\s*(?:[-:–—]\s*)?(.*)$`)
	bareNumberedLabel = regexp.MustCompile(`^(.+?)\s+(?:[-:#]+\s*)?([0-9]+(?:[.,][0-9]+)?)(?:\s*[-:–—]\s*(.*))?$`)
	filenameVolume    = regexp.MustCompile(`^[0-9]{1,3}(?:[.,][0-9]+)?$`)
)

// BookLabel separates explicitly numbered series labels from actual titles.
// An unlabelled number is interpreted as a volume only for an already known
// series, so titles such as "Catch 22" do not become invented series metadata.
type BookLabel struct {
	Title  string
	Series string
	Volume string
}

// ClassifyBookLabel recognizes explicit volume markers or a known series prefix.
// Translated leading articles are ignored when comparing known series names.
func ClassifyBookLabel(value, knownSeries string) BookLabel {
	value = strings.TrimSpace(value)
	if match := numberedLabel.FindStringSubmatch(value); match != nil {
		return BookLabel{Title: strings.TrimSpace(match[3]), Series: strings.Trim(match[1], " -:"), Volume: strings.ReplaceAll(match[2], ",", ".")}
	}
	if match := bareNumberedLabel.FindStringSubmatch(strings.TrimRight(value, ".")); match != nil && knownSeries != "" && seriesKey(match[1]) == seriesKey(knownSeries) {
		return BookLabel{Title: strings.TrimSpace(match[3]), Series: knownSeries, Volume: strings.ReplaceAll(match[2], ",", ".")}
	}
	if knownSeries != "" && seriesKey(value) == seriesKey(knownSeries) {
		return BookLabel{Series: knownSeries}
	}
	return BookLabel{Title: value}
}

func seriesKey(value string) string {
	value = NormalizeSearchText(value)
	words := strings.Fields(value)
	if len(words) > 1 {
		switch words[0] {
		case "the", "a", "an", "les", "le", "la", "l", "un", "une":
			words = words[1:]
		}
	}
	return strings.Join(words, " ")
}

// FilenameBookLabel extracts a possible series/volume/title from the filename.
// Known author segments are excluded before classifying the remaining label.
// A separate numeric segment between series and title is a reviewable volume
// clue even without existing series metadata. Bare numbers in titles remain
// untouched, and four-digit years are not treated as volume numbers.
func FilenameBookLabel(filename string, identity SearchIdentity) BookLabel {
	filename = strings.TrimSuffix(filename, filepath.Ext(filename))
	filename = strings.NewReplacer("_", " ", " – ", " - ", " — ", " - ").Replace(filename)
	for _, author := range identity.Authors {
		parts := strings.Split(filename, " - ")
		var remaining []string
		for _, part := range parts {
			if NormalizeSearchText(part) != NormalizeSearchText(author) {
				remaining = append(remaining, part)
			}
		}
		filename = strings.Join(remaining, " - ")
	}
	filename = fileNoise.ReplaceAllString(filename, " ")
	parts := strings.Split(filename, " - ")
	if len(parts) >= 3 && filenameVolume.MatchString(strings.TrimSpace(parts[1])) {
		series := strings.TrimSpace(parts[0])
		title := strings.TrimSpace(strings.Join(parts[2:], " - "))
		if series != "" && title != "" {
			volume, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(parts[1]), ",", "."), 64)
			return BookLabel{Title: title, Series: series, Volume: strconv.FormatFloat(volume, 'f', -1, 64)}
		}
	}
	return ClassifyBookLabel(filename, identity.Series)
}
