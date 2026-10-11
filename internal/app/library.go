package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"shelfmark/internal/metadata"
	"shelfmark/internal/settings"
)

// browse lists candidate library folders rooted at the requested directory.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}

	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	prefs, err := settings.Load(r.Context(), s.db)
	if err != nil {
		jsonError(w, err)
		return
	}

	p := browsePath(in.Path, prefs.LastFolder)

	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		jsonError(w, err)
		return
	}

	st, err := os.Stat(abs)
	if err != nil {
		jsonError(w, err)
		return
	}

	if !st.IsDir() {
		jsonError(w, fmt.Errorf("%s is not a directory", abs))
		return
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		jsonError(w, err)
		return
	}

	dirs := browseDirectories(abs, entries, prefs.IncludeHidden)
	parent := parentPath(abs)

	jsonOut(w, map[string]any{"path": abs, "parent": parent, "dirs": dirs})
}

// browsePath prefers the requested folder, then the saved one, then the home directory.
func browsePath(requested, saved string) string {
	path := requested
	if path == "" && saved != "" {
		path = saved
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			slog.Warn("saved library folder unavailable; opening home directory", "path", path)
			path = ""
		}
	}

	if path == "" {
		path, _ = os.UserHomeDir()
	}

	return path
}

// browseDirectories applies the hidden-file setting and returns names in UI order.
func browseDirectories(root string, entries []os.DirEntry, includeHidden bool) []map[string]string {
	dirs := make([]map[string]string, 0)
	for _, entry := range entries {
		if entry.IsDir() && (includeHidden || !strings.HasPrefix(entry.Name(), ".")) {
			dirs = append(dirs, map[string]string{"name": entry.Name(), "path": filepath.Join(root, entry.Name())})
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i]["name"]) < strings.ToLower(dirs[j]["name"])
	})

	return dirs
}

// parentPath returns the parent folder, or empty at the filesystem root.
func parentPath(path string) string {
	parent := filepath.Dir(path)
	if parent == path {
		return ""
	}

	return parent
}

// scan collects supported regular files and publishes a new index only after
// the traversal succeeds. Per-file read failures become warnings; persistent
// drafts are overlaid without changing the original books.
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root string `json:"root"`
	}

	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	if strings.TrimSpace(in.Root) == "" {
		jsonError(w, errors.New("choose a library folder"))
		return
	}

	// Hold the lifecycle lock through publication so a new job cannot start
	// between cancellation and replacing the scan. Join before taking the file
	// lock, because the old worker may still be finishing a metadata read.
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	s.stopBackgroundLocked()

	s.operations.Lock()
	defer s.operations.Unlock()

	prefs, err := settings.Load(r.Context(), s.db)
	if err != nil {
		jsonError(w, err)
		return
	}

	root, err := filepath.Abs(filepath.Clean(in.Root))
	if err != nil {
		jsonError(w, err)
		return
	}

	st, err := os.Stat(root)
	if err != nil {
		jsonError(w, fmt.Errorf("cannot access %q: %w", root, err))
		return
	}

	if !st.IsDir() {
		jsonError(w, fmt.Errorf("%q is not a directory", root))
		return
	}

	started := time.Now()
	slog.Info("library scan started", "root", root, "recursive", prefs.Recursive, "include_hidden", prefs.IncludeHidden)
	prefs.LastFolder = root
	if err := settings.Save(r.Context(), s.db, prefs); err != nil {
		jsonError(w, err)
		return
	}

	result, err := s.scanLibrary(r.Context(), root, prefs)
	if err != nil {
		jsonError(w, err)
		return
	}

	s.pathsMu.Lock()
	s.paths = result.paths
	s.root = root
	s.pathsMu.Unlock()

	sortBooks(result.books)
	slog.Info("library scan completed", "books", len(result.books), "warnings", len(result.warnings), "duration", time.Since(started))

	view := makeLibraryView(root, root, "", result.books)
	view.Warnings = result.warnings

	jsonOut(w, view)
}

// scanResult holds one scan's full replacement index before publication.
type scanResult struct {
	books    []book
	warnings []string
	paths    map[string]string
}

// scanLibrary walks the selected root and accumulates warnings without
// publishing partial results until the traversal finishes successfully.
func (s *Server) scanLibrary(ctx context.Context, root string, prefs settings.Preferences) (scanResult, error) {
	result := scanResult{books: make([]book, 0), warnings: make([]string, 0), paths: make(map[string]string)}
	supported := map[string]bool{".epub": true, ".fb2": true, ".cbz": true, ".pdf": true}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			slog.Warn("cannot access library entry; skipping", "path", path, "error", walkErr)
			result.warnings = append(result.warnings, fmt.Sprintf("%s: %v", path, walkErr))
			return nil
		}

		if entry.IsDir() {
			if shouldSkipDirectory(path, root, entry.Name(), prefs) {
				return filepath.SkipDir
			}
			return nil
		}

		if !supportedScanEntry(entry, path, prefs.IncludeHidden, supported) {
			return nil
		}

		b, warning, err := s.scanBook(ctx, path, entry)
		if warning != "" {
			result.warnings = append(result.warnings, warning)
		}

		if err != nil {
			return err
		}

		result.paths[idFor(path)] = path
		result.books = append(result.books, b)

		return nil
	})
	return result, err
}

// shouldSkipDirectory applies recursion and hidden-folder policy during scanning.
func shouldSkipDirectory(path, root, name string, prefs settings.Preferences) bool {
	return path != root && (!prefs.Recursive || (!prefs.IncludeHidden && strings.HasPrefix(name, ".")))
}

// supportedScanEntry filters regular, non-symlinked files to the supported formats.
func supportedScanEntry(entry os.DirEntry, path string, includeHidden bool, supported map[string]bool) bool {
	return (includeHidden || !strings.HasPrefix(entry.Name(), ".")) && entry.Type()&os.ModeSymlink == 0 && supported[strings.ToLower(filepath.Ext(path))]
}

// scanBook reads file metadata and overlays any stored draft for that path.
func (s *Server) scanBook(ctx context.Context, path string, entry os.DirEntry) (book, string, error) {
	info, err := entry.Info()
	if err != nil {
		slog.Warn("cannot inspect library file; skipping", "path", path, "error", err)
		return book{}, fmt.Sprintf("%s: %v", path, err), nil
	}

	slog.Debug("scanning book", "path", path)

	fileMeta, err := metadata.Read(path)
	warning := ""
	if err != nil {
		slog.Warn("book metadata unreadable; using filename", "path", path, "error", err)
		warning = fmt.Sprintf("%s: %v", path, err)
		fileMeta = map[string]any{
			"title":      strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			"authors":    []string{},
			"read_error": err.Error(),
		}
	}

	fp := fingerprint(path, info, fileMeta)
	draft, staged, err := s.db.ReadBook(ctx, path, fp, fileMeta)
	if err != nil {
		return book{}, warning, err
	}

	return makeBook(path, draft, fileMeta, staged), warning, nil
}

// fingerprint detects file or metadata changes between review and save.
// It is a freshness token, not a cryptographic hash of the complete book.
func fingerprint(path string, info os.FileInfo, m map[string]any) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%v", path, info.Size(), info.ModTime().UnixNano(), m)))
	return hex.EncodeToString(sum[:])
}

// idFor gives a path a stable opaque identifier for API requests.
func idFor(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:12])
}

// cover returns the current cover image for one scanned book.
func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}

	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}

	p := s.lookupPath(in.ID)
	if p == "" {
		jsonError(w, errors.New("book is not in the current library scan"))
		return
	}

	s.operations.RLock()
	defer s.operations.RUnlock()
	value, err := metadata.Cover(p)
	if err != nil {
		jsonError(w, err)
		return
	}

	jsonOut(w, map[string]string{"cover": value})
}

// lookupPath resolves an opaque API book ID to the current scan path.
func (s *Server) lookupPath(id string) string {
	s.pathsMu.RLock()
	defer s.pathsMu.RUnlock()
	return s.paths[id]
}
