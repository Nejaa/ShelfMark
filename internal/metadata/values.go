package metadata

import (
	"shelfmark/internal/library"
)

// Text converts scalar and JSON list metadata into a display string.
func Text(value any) string { return library.Text(value) }

// Strings normalizes native and JSON-decoded list values into strings.
func Strings(value any) []string { return library.Strings(value) }

// IsEqual compares values using the same empty/list rules as persisted drafts.
func IsEqual(a, b any) bool {
	return library.Equal(a, b)
}
