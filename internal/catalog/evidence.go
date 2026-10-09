package catalog

import "fmt"

// corroborateCandidates supplies specific titles from records tied by ISBN.
// Matching only an author's name is not enough to transfer another book's title.
func corroborateCandidates(request SearchRequest, candidates []Candidate) {
	for i := range candidates {
		candidate := &candidates[i]
		if specificCandidateTitle(request.Identity, *candidate) != "" {
			continue
		}
		for _, donor := range candidates {
			if donor.URL == candidate.URL || differentLanguage(request.Identity.Languages, donor.Language) {
				continue
			}
			title := specificCandidateTitle(request.Identity, donor)
			if title == "" || !sharedISBN(*candidate, donor) {
				continue
			}
			candidate.Evidence = append(candidate.Evidence, MetadataEvidence{Field: "title", Value: title, Source: donor.Source, URL: donor.URL})
			candidate.SourceNotes += fmt.Sprintf(" Specific title corroborated by %s; review the proposed value.", donor.Source)
			break
		}
	}
}

func sharedISBN(a, b Candidate) bool {
	for _, left := range append([]string{a.ISBN}, a.ISBNs...) {
		for _, right := range append([]string{b.ISBN}, b.ISBNs...) {
			if canonicalISBN(left) != "" && canonicalISBN(left) == canonicalISBN(right) {
				return true
			}
		}
	}
	return false
}
