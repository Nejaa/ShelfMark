package catalog

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"shelfmark/internal/library"
)

// MetadataEvidence is a corroborating clue, never an instruction to write.
// It travels with a candidate so selecting a cached result keeps its provenance.
type MetadataEvidence struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source string `json:"source"`
	URL    string `json:"url"`
}

// MetadataProposal includes the reasoning and ambiguity of each suggested field.
// Review fields remain unselected until the user chooses them or edits the value.
type MetadataProposal struct {
	Values   map[string]any
	Evidence map[string]string
	Review   map[string]bool
}

// Proposal uses the same reconciliation as the editor when staging selected data.
func Proposal(current map[string]any, candidate Candidate, filename string) map[string]any {
	return Reconcile(current, candidate, filename).Values
}

// Reconcile combines local, filename and catalog evidence. It preserves useful
// metadata when the catalog provides a series label, a different language or a
// work-level record whose edition fields cannot be tied to this file.
func Reconcile(current map[string]any, candidate Candidate, filename string) MetadataProposal {
	candidate = normalizeContributors(candidate)
	result := MetadataProposal{Values: library.Working(current, nil), Evidence: make(map[string]string), Review: make(map[string]bool)}
	set := func(name string, value any, reason string, review bool) {
		if library.Text(value) == "" {
			return
		}
		result.Values[name], result.Evidence[name], result.Review[name] = value, reason, review
	}
	source := candidate.Source
	if source == "" {
		source = "Catalog record"
	}
	identity := library.IdentifySearch(current, filepath.Base(filename))
	fileLabel := library.FilenameBookLabel(filepath.Base(filename), identity)
	knownSeries := identity.Series
	if knownSeries == "" {
		knownSeries = fileLabel.Series
	}
	localLabel := library.ClassifyBookLabel(library.Text(current["title"]), knownSeries)
	remoteLabel := library.ClassifyBookLabel(candidate.Title, knownSeries)
	if remoteLabel.Series == "" && fileLabel.Series != "" {
		if fromFilename := library.ClassifyBookLabel(candidate.Title, fileLabel.Series); fromFilename.Series != "" {
			remoteLabel = fromFilename
		}
	}
	localTitle := strings.TrimSpace(localLabel.Title)
	localSubtitle := strings.TrimSpace(library.Text(current["subtitle"]))
	classificationIdentity := identity
	classificationIdentity.Series = knownSeries
	if remoteLabel.Series != "" {
		classificationIdentity.Series = remoteLabel.Series
	}
	remoteTitle := specificCandidateTitle(classificationIdentity, candidate)
	localGeneric := localTitle == "" || placeholderTitle(localTitle)
	languageConflict := differentLanguage(identity.Languages, candidate.Language)

	// Start with provider values, then apply field-specific consistency rules.
	for name, value := range map[string]any{
		"publisher": candidate.Publisher, "pubdate": candidate.Pubdate,
		"isbn": candidate.ISBN, "tags": candidate.Categories, "comments": candidate.Description,
		"identifiers": candidate.Identifiers, "catalog_url": candidate.CatalogURL,
	} {
		local := library.Text(current[name])
		editionField := name == "publisher" || name == "pubdate" || name == "isbn"
		if editionField && (candidate.WorkRecord || languageConflict) && local != "" {
			result.Evidence[name] = "Existing edition value preserved; catalog record does not identify this edition reliably"
			continue
		}
		review := editionField && (candidate.WorkRecord || languageConflict)
		set(name, value, source, review)
	}
	if candidate.CatalogURL == "" {
		set("catalog_url", candidate.URL, source, false)
	}
	if len(identity.Languages) == 0 {
		set("languages", nonemptyList(languageKey(candidate.Language)), source, candidate.WorkRecord)
	} else if languageConflict {
		result.Evidence["languages"] = "Existing book language preserved; selected record describes another language"
	}

	// Contributor labels and known translator names are treated as role evidence.
	// Never guess that an unfamiliar person's name is an author or translator.
	var authors []string
	translators := slices.Clone(candidate.Translators)
	for _, author := range candidate.Authors {
		isTranslator := slices.ContainsFunc(library.Strings(current["translators"]), func(name string) bool { return authorSimilarity(name, author) >= .9 })
		if isTranslator {
			translators = append(translators, author)
		} else {
			authors = append(authors, author)
		}
	}
	set("authors", authors, source, len(identity.Authors) > 0 && !sameContributors(identity.Authors, authors))
	if len(translators) > 0 {
		translators = uniqueContributors(append(slices.Clone(library.Strings(current["translators"])), translators...))
		set("translators", translators, source+"; contributor roles reconciled with existing metadata", true)
	}

	// Local subtitles often hold the real title under a generated series heading.
	// Promote that title and clear its duplicate subtitle as one coherent proposal.
	promotedSubtitle := false
	switch {
	case localGeneric && !placeholderTitle(localSubtitle) && library.ClassifyBookLabel(localSubtitle, knownSeries).Title != "":
		set("title", localSubtitle, "Existing subtitle promoted; main title was a series/volume label", false)
		result.Values["subtitle"] = ""
		result.Evidence["subtitle"] = "Removed duplicate subtitle after promoting it to the main title"
		promotedSubtitle = true
	case remoteTitle == "" && !localGeneric:
		set("title", localTitle, "Existing specific title preserved; catalog title is a series/volume label", false)
	case languageConflict && !localGeneric:
		set("title", localTitle, "Existing title preserved in the book's language", false)
	case remoteTitle != "":
		set("title", remoteTitle, source+"; book title separated from series/volume", !localGeneric && textSimilarity(localTitle, remoteTitle) < .65)
		if candidate.Subtitle != "" && library.NormalizeSearchText(candidate.Subtitle) != library.NormalizeSearchText(remoteTitle) {
			set("subtitle", candidate.Subtitle, source, false)
		} else if library.NormalizeSearchText(localSubtitle) == library.NormalizeSearchText(remoteTitle) {
			result.Values["subtitle"] = ""
			result.Evidence["subtitle"] = "Removed duplicate of the proposed main title"
		}
	case localGeneric && fileLabel.Title != "":
		set("title", cleanFilenameTitle(fileLabel.Title, identity), "Specific title inferred from filename; verify spelling", true)
	default:
		result.Evidence["title"] = "No specific title could be established; existing value retained for review"
	}
	if !promotedSubtitle && library.Text(result.Values["title"]) == library.Text(current["title"]) && candidate.Subtitle != "" && library.NormalizeSearchText(candidate.Subtitle) != library.NormalizeSearchText(library.Text(result.Values["title"])) {
		set("subtitle", candidate.Subtitle, source, languageConflict)
	}

	// Explicit labels provide series evidence. Existing localized series names
	// are preferred over a catalog's translated equivalent.
	series, volume, reason := candidate.Series, candidate.SeriesIndex, source
	if remoteLabel.Series != "" {
		series, volume, reason = remoteLabel.Series, remoteLabel.Volume, source+"; numbered series label"
	}
	for _, label := range candidate.SeriesLabels {
		parts := library.ClassifyBookLabel(label, knownSeries)
		if parts.Series != "" && parts.Volume != "" {
			series, volume, reason = parts.Series, parts.Volume, source+"; edition series label"
			break
		}
	}
	if series == "" && fileLabel.Series != "" {
		series, volume, reason = fileLabel.Series, fileLabel.Volume, "Filename series/volume label"
	}
	if series == "" && localLabel.Series != "" {
		series, volume, reason = localLabel.Series, localLabel.Volume, "Existing numbered main title"
	}
	seriesConflict := identity.Series != "" && series != "" && library.ClassifyBookLabel(series, identity.Series).Series == ""
	if identity.Series == "" || seriesConflict {
		set("series", series, reason, seriesConflict || reason == "Filename series/volume label")
	}
	if volume != "" {
		conflict := identity.SeriesIndex != "" && !sameVolume(identity.SeriesIndex, volume)
		set("series_index", volume, reason, conflict || seriesConflict || reason == "Filename series/volume label")
	}
	set("original_title", candidate.OriginalTitle, source, library.Text(current["original_title"]) != "" && textSimilarity(library.Text(current["original_title"]), candidate.OriginalTitle) < .65)

	// Cross-record clues are restricted to title structure and explicit roles.
	// Publication details are never borrowed from a different edition.
	for _, hint := range candidate.Evidence {
		if !slices.Contains([]string{"title", "subtitle", "original_title", "series", "series_index"}, hint.Field) {
			continue
		}
		if hint.Field == "title" && (promotedSubtitle || (!localGeneric && (remoteTitle == "" || languageConflict))) {
			continue
		}
		if hint.Field != "title" && library.Text(result.Values[hint.Field]) != "" {
			continue
		}
		set(hint.Field, hint.Value, "Corroborated by "+hint.Source+": "+hint.URL, true)
	}
	if library.Text(result.Values["title"]) != library.Text(current["title"]) && library.NormalizeSearchText(library.Text(result.Values["subtitle"])) == library.NormalizeSearchText(library.Text(result.Values["title"])) {
		result.Values["subtitle"] = ""
		result.Evidence["subtitle"] = "Removed duplicate of the proposed main title"
		result.Review["subtitle"] = result.Review["title"]
	}
	if candidate.WorkRecord {
		for _, name := range []string{"publisher", "pubdate", "isbn", "languages"} {
			if result.Review[name] {
				result.Evidence[name] += "; work-level data, verify the edition"
			}
		}
	}
	return result
}

func specificCandidateTitle(identity library.SearchIdentity, candidate Candidate) string {
	label := library.ClassifyBookLabel(candidate.Title, identity.Series)
	if label.Title != "" && !placeholderTitle(label.Title) {
		return label.Title
	}
	if subtitle := strings.TrimSpace(candidate.Subtitle); !placeholderTitle(subtitle) && library.ClassifyBookLabel(subtitle, identity.Series).Title != "" {
		return subtitle
	}
	return ""
}

func placeholderTitle(value string) bool {
	switch library.NormalizeSearchText(value) {
	case "", "unknown", "untitled", "unknown title", "sans titre", "book", "ebook", "title", "titre":
		return true
	}
	return false
}

func differentLanguage(local []string, remote string) bool {
	return len(local) > 0 && remote != "" && !slices.ContainsFunc(local, func(value string) bool { return languageKey(value) == languageKey(remote) })
}

func nonemptyList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}

func sameContributors(local, remote []string) bool {
	if len(local) != len(remote) {
		return false
	}
	used := make([]bool, len(remote))
	for _, name := range local {
		index := slices.IndexFunc(remote, func(other string) bool { return authorSimilarity(name, other) >= .9 })
		if index < 0 || used[index] {
			return false
		}
		used[index] = true
	}
	return true
}

func uniqueContributors(names []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := library.NormalizeSearchText(name)
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

func sameVolume(a, b string) bool {
	left, leftErr := strconv.ParseFloat(strings.ReplaceAll(a, ",", "."), 64)
	right, rightErr := strconv.ParseFloat(strings.ReplaceAll(b, ",", "."), 64)
	return leftErr == nil && rightErr == nil && left == right
}

func cleanFilenameTitle(title string, identity library.SearchIdentity) string {
	values := map[string]any{"authors": identity.Authors, "series": identity.Series}
	guessed := library.IdentifySearch(values, title+".epub")
	if len(guessed.Titles) > 0 {
		return guessed.Titles[0]
	}
	return strings.TrimSpace(title)
}
