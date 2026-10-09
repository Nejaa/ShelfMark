package catalog

import (
	"slices"
	"strings"
)

// normalizeContributors recognizes explicit role annotations found in catalogs.
// It never assigns a role from name shape or prose mentioning a person.
func normalizeContributors(candidate Candidate) Candidate {
	var authors []string
	translators := slices.Clone(candidate.Translators)
	for _, name := range candidate.Authors {
		name = strings.TrimSpace(name)
		lower := strings.ToLower(name)
		role := false
		for _, marker := range []string{"(translator)", "[translator]", "(traducteur)", "(traductrice)", "(translation)"} {
			if strings.HasSuffix(lower, marker) {
				name = strings.TrimSpace(name[:len(name)-len(marker)])
				role = true
				break
			}
		}
		if role {
			translators = append(translators, name)
		} else {
			authors = append(authors, name)
		}
	}
	candidate.Authors, candidate.Translators = uniqueContributors(authors), uniqueContributors(translators)
	return candidate
}
