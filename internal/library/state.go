// Package library defines the persisted state of books in a user's collection.
package library

// State holds the last observed file metadata and any staged metadata changes.
type State struct {
	// Path is the book's canonical path within the current library.
	Path string

	// Fingerprint identifies the file contents last observed for this state.
	Fingerprint string

	// Metadata is the last metadata snapshot read from the file.
	Metadata map[string]any

	// Staged contains only unsaved changes layered over Metadata.
	Staged map[string]any
}
