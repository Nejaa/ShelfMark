package catalog

import (
	"slices"
	"strings"
	"unicode"

	"shelfmark/internal/library"
)

// matchRank keeps the separate title, author, and ISBN signals used to score a candidate.
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
	titles := expectedTitles(request, identity)
	titleScore, matchedTitle := bestTitleMatch(titles, candidate)

	rank := matchRank{
		Title:  titleScore,
		Author: authorMatchScore(identity.Authors, candidate.Authors),
		ISBN:   hasMatchingISBN(request.ISBNs, candidate),
	}

	rank.Total = weightedEvidenceScore(identity, candidate, rank)
	applyRankPenalties(&rank, identity, matchedTitle, candidate)
	rank.Total = min(max(rank.Total, 0), 1)
	rank.Strong = rank.ISBN || (rank.Title >= .88 && (len(identity.Authors) == 0 || rank.Author >= .75) && rank.Total >= .85)

	return rank
}

// expectedTitles strips embedded author text from custom free-text queries before title matching.
func expectedTitles(request SearchRequest, identity library.SearchIdentity) []string {
	if !request.CustomQuery || canonicalISBN(request.Query) != "" {
		return identity.Titles
	}

	// Explicit edits change the expected title too. Remove known author text
	// from a plain query without interpreting arbitrary provider syntax.
	title := library.NormalizeSearchText(request.Query)
	for _, author := range identity.Authors {
		title = strings.TrimSpace(strings.ReplaceAll(" "+title+" ", " "+library.NormalizeSearchText(author)+" ", " "))
	}

	return []string{strings.TrimSpace(title)}
}

// bestTitleMatch checks title, title+subtitle, and original title against every expected title.
func bestTitleMatch(titles []string, candidate Candidate) (float64, string) {
	var best float64
	matchedTitle := ""

	for _, local := range titles {
		for _, remote := range []string{candidate.Title, candidate.Title + " " + candidate.Subtitle, candidate.OriginalTitle} {
			if similarity := textSimilarity(local, remote); similarity > best {
				best, matchedTitle = similarity, local
			}
		}
	}

	return best, matchedTitle
}

// authorMatchScore averages each local author's best unused candidate match.
func authorMatchScore(local, remote []string) float64 {
	if len(local) == 0 {
		return 0
	}

	used := make([]bool, len(remote))
	total := 0.0

	for _, author := range local {
		best, index := bestUnusedAuthorMatch(author, remote, used)
		if index >= 0 {
			used[index] = true
		}
		total += best
	}

	return total / float64(len(local))
}

// bestUnusedAuthorMatch prevents a single candidate author from satisfying multiple local authors.
func bestUnusedAuthorMatch(author string, candidates []string, used []bool) (float64, int) {
	best, bestIndex := 0.0, -1

	for index, candidate := range candidates {
		if used[index] {
			continue
		}

		if score := authorSimilarity(author, candidate); score > best {
			best, bestIndex = score, index
		}
	}
	return best, bestIndex
}

// hasMatchingISBN accepts any canonical match across provider primary and alternate ISBN fields.
func hasMatchingISBN(local []string, candidate Candidate) bool {
	remote := append([]string{candidate.ISBN}, candidate.ISBNs...)

	for _, expected := range local {
		if canonicalISBN(expected) == "" {
			continue
		}

		for _, identifier := range remote {
			if canonicalISBN(expected) == canonicalISBN(identifier) {
				return true
			}
		}
	}
	return false
}

// weightedEvidenceScore lets title dominate while authors and language refine close matches.
func weightedEvidenceScore(identity library.SearchIdentity, candidate Candidate, rank matchRank) float64 {
	// Missing expected fields contribute zero; fields absent from the local
	// identity contribute no weight. Exact titles can still rank without authors.
	weight, total := .7, .7*rank.Title
	if len(identity.Authors) > 0 {
		weight += .25
		total += .25 * rank.Author
	}

	if len(identity.Languages) > 0 {
		weight += .05
		if hasMatchingLanguage(identity.Languages, candidate) {
			total += .05
		}
	}

	return total / weight
}

// hasMatchingLanguage treats provider language aliases as equivalent normalized codes.
func hasMatchingLanguage(local []string, candidate Candidate) bool {
	remote := append([]string{candidate.Language}, candidate.Languages...)

	for _, expected := range local {
		if slices.ContainsFunc(remote, func(value string) bool {
			return languageKey(expected) != "" && languageKey(expected) == languageKey(value)
		}) {
			return true
		}
	}

	return false
}

// applyRankPenalties dampens plausible-but-wrong matches like adjacent series volumes.
func applyRankPenalties(rank *matchRank, identity library.SearchIdentity, matchedTitle string, candidate Candidate) {
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

// editSimilarity computes normalized Levenshtein similarity on Unicode code points.
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

	matched := matchedAuthorTokens(a, b)

	if len(a)+len(b) > 0 {
		best = max(best, 2*matched/float64(len(a)+len(b)))
	}

	return best
}

// matchedAuthorTokens scores partial author matches while keeping one-to-one token pairing.
func matchedAuthorTokens(a, b []string) float64 {
	used := make([]bool, len(b))
	matched := 0.0

	for _, word := range a {
		index, score := bestAuthorTokenMatch(word, b, used)
		if index >= 0 {
			used[index] = true
			matched += score
		}
	}

	return matched
}

// bestAuthorTokenMatch treats initials as weaker but still meaningful token matches.
func bestAuthorTokenMatch(word string, candidates []string, used []bool) (int, float64) {
	bestIndex, bestScore := -1, 0.0

	for index, candidate := range candidates {
		if used[index] {
			continue
		}

		score := authorTokenSimilarity(word, candidate)
		if score >= .75 && score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	return bestIndex, bestScore
}

// authorTokenSimilarity boosts single-letter initials that align with a full name token.
func authorTokenSimilarity(left, right string) float64 {
	score := editSimilarity(left, right)
	if isInitialMatch(left, right) {
		return .9
	}

	return score
}

// isInitialMatch accepts single-letter tokens when their first rune matches.
func isInitialMatch(left, right string) bool {
	leftRunes, rightRunes := []rune(left), []rune(right)
	return len(leftRunes) > 0 && len(rightRunes) > 0 &&
		(len(leftRunes) == 1 || len(rightRunes) == 1) && leftRunes[0] == rightRunes[0]
}

// differentNumbers rejects near-title matches that disagree on explicit numeric labels.
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
		return isbn10To13(value)
	}

	if len(value) != 13 || (!strings.HasPrefix(value, "978") && !strings.HasPrefix(value, "979")) {
		return ""
	}

	if validISBN13(value) {
		return value
	}

	return ""
}

// isbn10To13 validates ISBN-10 checksums before converting to a canonical ISBN-13.
func isbn10To13(value string) string {
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

// validISBN13 enforces the EAN-13 checksum for provider-supplied identifiers.
func validISBN13(value string) bool {
	sum := 0

	for index, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}

		sum += int(digit-'0') * (1 + 2*(index%2))
	}

	return sum%10 == 0
}

// languageKey normalizes common aliases to a short language code used for matching.
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
