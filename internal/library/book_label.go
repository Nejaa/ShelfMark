package library

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	numberedLabel     = regexp.MustCompile(`(?i)^(.+?)\s+(?:[-:]\s*)?(?:t(?:ome)?|vol(?:ume)?\.?|book|livre|#)\s*([0-9]+(?:[.,][0-9]+)?)\s*(?:[-:–—]\s*)?(.*)$`)
	bareNumberedLabel = regexp.MustCompile(`^(.+?)\s+(?:[-:#]+\s*)?([0-9]+(?:[.,][0-9]+)?)(?:\s*[-:–—]\s*(.*))?$`)
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
	return ClassifyBookLabel(filename, identity.Series)
}
