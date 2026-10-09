package catalog

import (
	"slices"
	"strings"
	"unicode"

	"shelfmark/internal/library"
)

type matchRank struct {
	Total  float64
	Title  float64
	Author float64
	ISBN   bool
	Strong bool
}

// rankCandidate measures independent evidence instead of counting query words
// inside a title (which incorrectly treats author names as missing title words).
// Scores are bounded heuristics, not probabilities or permission to apply data.
func rankCandidate(request SearchRequest, candidate Candidate) matchRank {
	identity := request.Identity
	titles := identity.Titles
	if request.CustomQuery && canonicalISBN(request.Query) == "" {
		// Explicit edits change the expected title too. Remove known author text
		// from a plain query without interpreting arbitrary provider syntax.
		title := library.NormalizeSearchText(request.Query)
		for _, author := range identity.Authors {
			title = strings.TrimSpace(strings.ReplaceAll(" "+title+" ", " "+library.NormalizeSearchText(author)+" ", " "))
		}
		titles = []string{strings.TrimSpace(title)}
	}
	var rank matchRank
	matchedTitle := ""
	for _, local := range titles {
		for _, remote := range []string{candidate.Title, candidate.Title + " " + candidate.Subtitle, candidate.OriginalTitle} {
			similarity := textSimilarity(local, remote)
			if similarity > rank.Title {
				rank.Title = similarity
				matchedTitle = local
			}
		}
	}
	usedAuthors := make([]bool, len(candidate.Authors))
	for _, author := range identity.Authors {
		best, index := 0.0, -1
		for j, remote := range candidate.Authors {
			if usedAuthors[j] {
				continue
			}
			similarity := authorSimilarity(author, remote)
			if similarity > best {
				best, index = similarity, j
			}
		}
		if index >= 0 {
			usedAuthors[index] = true
		}
		rank.Author += best
	}
	if len(identity.Authors) > 0 {
		rank.Author /= float64(len(identity.Authors))
	}
	for _, local := range request.ISBNs {
		for _, remote := range append([]string{candidate.ISBN}, candidate.ISBNs...) {
			if canonicalISBN(local) != "" && canonicalISBN(local) == canonicalISBN(remote) {
				rank.ISBN = true
			}
		}
	}

	// Missing expected fields contribute zero; fields absent from the local
	// identity contribute no weight. Exact titles can still rank without authors.
	weight := .7
	total := .7 * rank.Title
	if len(identity.Authors) > 0 {
		weight += .25
		total += .25 * rank.Author
	}
	if len(identity.Languages) > 0 {
		weight += .05
		for _, local := range identity.Languages {
			if slices.ContainsFunc(append([]string{candidate.Language}, candidate.Languages...), func(remote string) bool {
				return languageKey(local) != "" && languageKey(local) == languageKey(remote)
			}) {
				total += .05
				break
			}
		}
	}
	rank.Total = total / weight
	if len(identity.Authors) > 0 && len(candidate.Authors) > 0 && rank.Author < .35 {
		rank.Total *= .65
	}
	// Keep numbers meaningful: similar names for different numbered books
	// must not outrank the correct volume merely because most letters match.
	if differentNumbers(matchedTitle, candidate.Title+" "+candidate.Subtitle) {
		rank.Total *= .75
	}
	if identity.SeriesIndex != "" && candidate.SeriesIndex != "" && textSimilarity(identity.Series, candidate.Series) >= .8 && identity.SeriesIndex != candidate.SeriesIndex {
		rank.Total *= .75
	}
	if rank.ISBN {
		rank.Total = .95 + .05*rank.Total
	}
	rank.Total = min(max(rank.Total, 0), 1)
	rank.Strong = rank.ISBN || (rank.Title >= .88 && (len(identity.Authors) == 0 || rank.Author >= .75) && rank.Total >= .85)
	return rank
}

// textSimilarity combines rune-based Levenshtein distance with token overlap.
// The token component tolerates reordered words; edit distance tolerates typos.
func textSimilarity(left, right string) float64 {
	left, right = library.NormalizeSearchText(left), library.NormalizeSearchText(right)
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	edit := editSimilarity(left, right)
	a, b := strings.Fields(left), strings.Fields(right)
	slices.Sort(a)
	slices.Sort(b)
	a, b = slices.Compact(a), slices.Compact(b)
	hits := 0
	for _, word := range a {
		if slices.Contains(b, word) {
			hits++
		}
	}
	overlap := 2 * float64(hits) / float64(len(a)+len(b))
	return .6*edit + .4*overlap
}

func editSimilarity(left, right string) float64 {
	a, b := []rune(left), []rune(right)
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(a) == 0 {
		return 0
	}
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i, x := range a {
		diagonal := row[0]
		row[0] = i + 1
		for j, y := range b {
			above := row[j+1]
			cost := 0
			if x != y {
				cost = 1
			}
			row[j+1] = min(row[j+1]+1, row[j]+1, diagonal+cost)
			diagonal = above
		}
	}
	return 1 - float64(row[len(b)])/float64(len(a))
}

// authorSimilarity handles reversed surname order and initials without awarding
// the same candidate author repeatedly. Initials receive slightly less credit
// than full names, and a shared surname alone is insufficient for a strong hit.
func authorSimilarity(left, right string) float64 {
	left, right = library.NormalizeSearchText(left), library.NormalizeSearchText(right)
	best := textSimilarity(left, right)
	a, b := strings.Fields(left), strings.Fields(right)
	used := make([]bool, len(b))
	matched := 0.0
	for _, word := range a {
		index, score := -1, 0.0
		for j, other := range b {
			if used[j] {
				continue
			}
			similarity := editSimilarity(word, other)
			if word != "" && other != "" && (len([]rune(word)) == 1 || len([]rune(other)) == 1) && []rune(word)[0] == []rune(other)[0] {
				similarity = .9
			}
			if similarity >= .75 && similarity > score {
				index, score = j, similarity
			}
		}
		if index >= 0 {
			used[index] = true
			matched += score
		}
	}
	if len(a)+len(b) > 0 {
		best = max(best, 2*matched/float64(len(a)+len(b)))
	}
	return best
}

func differentNumbers(left, right string) bool {
	numbers := func(value string) []string {
		var result []string
		for _, word := range strings.Fields(library.NormalizeSearchText(value)) {
			if strings.IndexFunc(word, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
				result = append(result, word)
			}
		}
		slices.Sort(result)
		return result
	}
	a, b := numbers(left), numbers(right)
	return len(a) > 0 && len(b) > 0 && !slices.Equal(a, b)
}

// canonicalISBN compares ISBN-10 and ISBN-13 representations of the same book.
// Reject malformed provider identifiers instead of using substring agreement.
func canonicalISBN(value string) string {
	value = strings.ToUpper(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == 'X' || r == 'x' {
			return r
		}
		return -1
	}, value))
	if len(value) == 10 {
		sum := 0
		for i, r := range value {
			if r == 'X' && i == 9 {
				sum += 10
				continue
			}
			if r < '0' || r > '9' {
				return ""
			}
			sum += (10 - i) * int(r-'0')
		}
		if sum%11 != 0 {
			return ""
		}
		value = "978" + value[:9]
		sum = 0
		for i, r := range value {
			sum += int(r-'0') * (1 + 2*(i%2))
		}
		return value + string(rune('0'+(10-sum%10)%10))
	}
	if len(value) != 13 || (!strings.HasPrefix(value, "978") && !strings.HasPrefix(value, "979")) {
		return ""
	}
	sum := 0
	for i, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
		sum += int(r-'0') * (1 + 2*(i%2))
	}
	if sum%10 != 0 {
		return ""
	}
	return value
}

func languageKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "fre", "fra", "french":
		return "fr"
	case "eng", "english":
		return "en"
	case "deu", "ger", "german":
		return "de"
	case "spa", "spanish":
		return "es"
	case "ita", "italian":
		return "it"
	case "por", "portuguese":
		return "pt"
	case "dut", "nld", "dutch":
		return "nl"
	}
	return strings.Split(strings.ReplaceAll(value, "_", "-"), "-")[0]
}
