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

// filenameSeriesEvidence marks filename-derived series clues so the UI can flag them for review.
const filenameSeriesEvidence = "Filename series/volume label"

// Proposal uses the same reconciliation as the editor when staging selected data.
func Proposal(current map[string]any, candidate Candidate, filename string) map[string]any {
	return Reconcile(current, candidate, filename).Values
}

// Reconcile combines local, filename and catalog evidence. It preserves useful
// metadata when the catalog provides a series label, a different language or a
// work-level record whose edition fields cannot be tied to this file.
func Reconcile(current map[string]any, candidate Candidate, filename string) MetadataProposal {
	proposal := newProposalContext(current, candidate, filename)
	proposal.applyProviderFields()
	proposal.applyContributors()
	promotedSubtitle := proposal.applyTitle()
	proposal.applySeries()
	proposal.applyCorroboratedEvidence(promotedSubtitle)
	proposal.appendWorkRecordNotes()
	return proposal.result
}

// proposalContext keeps local, filename, and remote interpretations aligned while reconciling.
type proposalContext struct {
	current          map[string]any
	candidate        Candidate
	result           MetadataProposal
	source           string
	identity         library.SearchIdentity
	fileLabel        library.BookLabel
	knownSeries      string
	localLabel       library.BookLabel
	remoteLabel      library.BookLabel
	localTitle       string
	localSubtitle    string
	remoteTitle      string
	localGeneric     bool
	languageConflict bool
}

// newProposalContext precomputes the local and remote title/series interpretations used by Reconcile.
func newProposalContext(current map[string]any, candidate Candidate, filename string) *proposalContext {
	candidate = normalizeContributors(candidate)
	file := filepath.Base(filename)
	identity := library.IdentifySearch(current, file)
	fileLabel := library.FilenameBookLabel(file, identity)
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
	classificationIdentity := identity
	classificationIdentity.Series = knownSeries
	if remoteLabel.Series != "" {
		classificationIdentity.Series = remoteLabel.Series
	}

	source := candidate.Source
	if source == "" {
		source = "Catalog record"
	}

	localTitle := strings.TrimSpace(localLabel.Title)

	return &proposalContext{
		current: current, candidate: candidate, source: source,
		result:   MetadataProposal{Values: library.Working(current, nil), Evidence: make(map[string]string), Review: make(map[string]bool)},
		identity: identity, fileLabel: fileLabel, knownSeries: knownSeries,
		localLabel: localLabel, remoteLabel: remoteLabel,
		localTitle: localTitle, localSubtitle: strings.TrimSpace(library.Text(current["subtitle"])),
		remoteTitle:      specificCandidateTitle(classificationIdentity, candidate),
		localGeneric:     localTitle == "" || placeholderTitle(localTitle),
		languageConflict: differentLanguage(identity.Languages, candidate.Language),
	}
}

// set writes a proposed field only when the candidate value is non-empty text.
func (p *proposalContext) set(name string, value any, reason string, review bool) {
	if library.Text(value) == "" {
		return
	}

	p.result.Values[name], p.result.Evidence[name], p.result.Review[name] = value, reason, review
}

// applyProviderFields copies edition-safe scalar fields and preserves risky local edition data.
func (p *proposalContext) applyProviderFields() {
	for name, value := range map[string]any{
		"publisher": p.candidate.Publisher, "pubdate": p.candidate.Pubdate,
		"isbn": p.candidate.ISBN, "tags": p.candidate.Categories, "comments": p.candidate.Description,
		"identifiers": p.candidate.Identifiers, "catalog_url": p.candidate.CatalogURL,
	} {
		local := library.Text(p.current[name])
		editionField := name == "publisher" || name == "pubdate" || name == "isbn"
		if editionField && (p.candidate.WorkRecord || p.languageConflict) && local != "" {
			p.result.Evidence[name] = "Existing edition value preserved; catalog record does not identify this edition reliably"
			continue
		}

		p.set(name, value, p.source, editionField && (p.candidate.WorkRecord || p.languageConflict))
	}

	if p.candidate.CatalogURL == "" {
		p.set("catalog_url", p.candidate.URL, p.source, false)
	}

	if len(p.identity.Languages) == 0 {
		p.set("languages", nonemptyList(languageKey(p.candidate.Language)), p.source, p.candidate.WorkRecord)
	} else if p.languageConflict {
		p.result.Evidence["languages"] = "Existing book language preserved; selected record describes another language"
	}
}

// applyContributors merges explicit translator roles without discarding local translator data.
func (p *proposalContext) applyContributors() {
	var authors []string
	translators := slices.Clone(p.candidate.Translators)
	currentTranslators := library.Strings(p.current["translators"])

	for _, author := range p.candidate.Authors {
		if isKnownTranslator(author, currentTranslators) {
			translators = append(translators, author)
			continue
		}

		authors = append(authors, author)
	}

	p.set("authors", authors, p.source, len(p.identity.Authors) > 0 && !sameContributors(p.identity.Authors, authors))

	if len(translators) > 0 {
		translators = uniqueContributors(append(slices.Clone(currentTranslators), translators...))
		p.set("translators", translators, p.source+"; contributor roles reconciled with existing metadata", true)
	}
}

// isKnownTranslator treats a provider author as a translator only when local metadata already supports it.
func isKnownTranslator(author string, translators []string) bool {
	return slices.ContainsFunc(translators, func(name string) bool {
		return authorSimilarity(name, author) >= .9
	})
}

// applyTitle preserves useful local titles when provider titles look generic, translated, or series-only.
func (p *proposalContext) applyTitle() bool {
	promotedSubtitle := false

	switch {
	case p.localGeneric && !placeholderTitle(p.localSubtitle) && library.ClassifyBookLabel(p.localSubtitle, p.knownSeries).Title != "":
		p.set("title", p.localSubtitle, "Existing subtitle promoted; main title was a series/volume label", false)
		p.result.Values["subtitle"] = ""
		p.result.Evidence["subtitle"] = "Removed duplicate subtitle after promoting it to the main title"
		promotedSubtitle = true
	case p.remoteTitle == "" && !p.localGeneric:
		p.set("title", p.localTitle, "Existing specific title preserved; catalog title is a series/volume label", false)
	case p.languageConflict && !p.localGeneric:
		p.set("title", p.localTitle, "Existing title preserved in the book's language", false)
	case p.remoteTitle != "":
		p.set("title", p.remoteTitle, p.source+"; book title separated from series/volume", !p.localGeneric && textSimilarity(p.localTitle, p.remoteTitle) < .65)
		p.applyRemoteSubtitle()
	case p.localGeneric && p.fileLabel.Title != "":
		p.set("title", cleanFilenameTitle(p.fileLabel.Title, p.identity), "Specific title inferred from filename; verify spelling", true)
	default:
		p.result.Evidence["title"] = "No specific title could be established; existing value retained for review"
	}

	if !promotedSubtitle && library.Text(p.result.Values["title"]) == library.Text(p.current["title"]) &&
		p.candidate.Subtitle != "" &&
		library.NormalizeSearchText(p.candidate.Subtitle) != library.NormalizeSearchText(library.Text(p.result.Values["title"])) {
		p.set("subtitle", p.candidate.Subtitle, p.source, p.languageConflict)
	}

	return promotedSubtitle
}

// applyRemoteSubtitle keeps catalog subtitles only when they add information beyond the chosen title.
func (p *proposalContext) applyRemoteSubtitle() {
	if p.candidate.Subtitle != "" && library.NormalizeSearchText(p.candidate.Subtitle) != library.NormalizeSearchText(p.remoteTitle) {
		p.set("subtitle", p.candidate.Subtitle, p.source, false)
	} else if library.NormalizeSearchText(p.localSubtitle) == library.NormalizeSearchText(p.remoteTitle) {
		p.result.Values["subtitle"] = ""
		p.result.Evidence["subtitle"] = "Removed duplicate of the proposed main title"
	}
}

// applySeries reconciles numbered series data from provider, filename, and existing metadata.
func (p *proposalContext) applySeries() {
	series, volume, reason := p.candidate.Series, p.candidate.SeriesIndex, p.source

	if p.remoteLabel.Series != "" {
		series, volume, reason = p.remoteLabel.Series, p.remoteLabel.Volume, p.source+"; numbered series label"
	}

	series, volume, reason = p.seriesLabelValues(series, volume, reason)

	if series == "" && p.fileLabel.Series != "" {
		series, volume, reason = p.fileLabel.Series, p.fileLabel.Volume, filenameSeriesEvidence
	}

	if series == "" && p.localLabel.Series != "" {
		series, volume, reason = p.localLabel.Series, p.localLabel.Volume, "Existing numbered main title"
	}

	volumeReason := reason
	if volume == "" && p.fileLabel.Volume != "" && library.ClassifyBookLabel(p.fileLabel.Series, series).Series != "" {
		volume, volumeReason = p.fileLabel.Volume, filenameSeriesEvidence
	}

	p.applySeriesValues(series, volume, reason, volumeReason)
	p.set("original_title", p.candidate.OriginalTitle, p.source,
		library.Text(p.current["original_title"]) != "" &&
			textSimilarity(library.Text(p.current["original_title"]), p.candidate.OriginalTitle) < .65)
}

// seriesLabelValues prefers explicit series labels emitted by provider edition metadata.
func (p *proposalContext) seriesLabelValues(series, volume, reason string) (string, string, string) {
	for _, label := range p.candidate.SeriesLabels {
		parts := library.ClassifyBookLabel(label, p.knownSeries)
		if parts.Series != "" && parts.Volume != "" {
			return parts.Series, parts.Volume, p.source + "; edition series label"
		}

		if series == "" && strings.TrimSpace(label) != "" {
			series, reason = strings.TrimSpace(label), p.source+"; edition series label"
		}
	}

	return series, volume, reason
}

// applySeriesValues marks conflicting series data for review instead of silently overwriting it.
func (p *proposalContext) applySeriesValues(series, volume, reason, volumeReason string) {
	seriesConflict := p.identity.Series != "" && series != "" &&
		library.ClassifyBookLabel(series, p.identity.Series).Series == ""

	if p.identity.Series == "" || seriesConflict {
		p.set("series", series, reason, seriesConflict || reason == filenameSeriesEvidence)
	}

	if volume == "" {
		return
	}

	conflict := p.identity.SeriesIndex != "" && !sameVolume(p.identity.SeriesIndex, volume)

	if p.fileLabel.Volume != "" && library.ClassifyBookLabel(p.fileLabel.Series, series).Series != "" &&
		!sameVolume(p.fileLabel.Volume, volume) {
		conflict = true
	}

	p.set("series_index", volume, volumeReason, conflict || seriesConflict || volumeReason == filenameSeriesEvidence)
}

// applyCorroboratedEvidence uses cross-record hints only for fields the main record left ambiguous.
func (p *proposalContext) applyCorroboratedEvidence(promotedSubtitle bool) {
	for _, hint := range p.candidate.Evidence {
		if !slices.Contains([]string{"title", "subtitle", "original_title", "series", "series_index"}, hint.Field) {
			continue
		}

		if hint.Field == "title" && (promotedSubtitle || !p.localGeneric && (p.remoteTitle == "" || p.languageConflict)) {
			continue
		}

		if hint.Field != "title" && library.Text(p.result.Values[hint.Field]) != "" {
			continue
		}

		p.set(hint.Field, hint.Value, "Corroborated by "+hint.Source+": "+hint.URL, true)
	}

	if library.Text(p.result.Values["title"]) != library.Text(p.current["title"]) &&
		library.NormalizeSearchText(library.Text(p.result.Values["subtitle"])) == library.NormalizeSearchText(library.Text(p.result.Values["title"])) {
		p.result.Values["subtitle"] = ""
		p.result.Evidence["subtitle"] = "Removed duplicate of the proposed main title"
		p.result.Review["subtitle"] = p.result.Review["title"]
	}
}

// appendWorkRecordNotes reminds reviewers when work-level data might describe another edition.
func (p *proposalContext) appendWorkRecordNotes() {
	if !p.candidate.WorkRecord {
		return
	}

	for _, name := range []string{"publisher", "pubdate", "isbn", "languages"} {
		if p.result.Review[name] {
			p.result.Evidence[name] += "; work-level data, verify the edition"
		}
	}
}

// specificCandidateTitle extracts the book title once series/volume labels are removed.
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

// placeholderTitle recognizes generic placeholders that should not displace a real title.
func placeholderTitle(value string) bool {
	switch library.NormalizeSearchText(value) {
	case "", "unknown", "untitled", "unknown title", "sans titre", "book", "ebook", "title", "titre":
		return true
	}

	return false
}

// differentLanguage reports whether the candidate language conflicts with known local languages.
func differentLanguage(local []string, remote string) bool {
	return len(local) > 0 && remote != "" && !slices.ContainsFunc(local, func(value string) bool { return languageKey(value) == languageKey(remote) })
}

// nonemptyList wraps a single normalized value for list-valued metadata fields.
func nonemptyList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return []string{value}
}

// sameContributors compares contributor lists with authorSimilarity instead of exact text equality.
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

// uniqueContributors preserves first-seen display names while deduplicating by normalized form.
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

// sameVolume compares decimal-looking volume labels numerically so 2 and 2,0 match.
func sameVolume(a, b string) bool {
	left, leftErr := strconv.ParseFloat(strings.ReplaceAll(a, ",", "."), 64)
	right, rightErr := strconv.ParseFloat(strings.ReplaceAll(b, ",", "."), 64)
	return leftErr == nil && rightErr == nil && left == right
}

// cleanFilenameTitle reuses filename parsing to strip residual author and series text from guesses.
func cleanFilenameTitle(title string, identity library.SearchIdentity) string {
	values := map[string]any{"authors": identity.Authors, "series": identity.Series}
	guessed := library.IdentifySearch(values, title+".epub")
	if len(guessed.Titles) > 0 {
		return guessed.Titles[0]
	}

	return strings.TrimSpace(title)
}
