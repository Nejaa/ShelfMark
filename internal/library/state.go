// Package library defines the persisted state of books in a user's collection.
package library

// State holds the last observed file metadata and any staged metadata changes.
type State struct {
	Path        string
	Fingerprint string
	Metadata    map[string]any
	Staged      map[string]any
}
