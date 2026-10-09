package app

import (
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
	p := in.Path
	if p == "" && prefs.LastFolder != "" {
		p = prefs.LastFolder
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			slog.Warn("saved library folder unavailable; opening home directory", "path", p)
			p = ""
		}
	}

	if p == "" {
		p, _ = os.UserHomeDir()
	}

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

	dirs := make([]map[string]string, 0)
	for _, e := range entries {
		if e.IsDir() && (prefs.IncludeHidden || !strings.HasPrefix(e.Name(), ".")) {
			dirs = append(dirs, map[string]string{"name": e.Name(), "path": filepath.Join(abs, e.Name())})
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i]["name"]) < strings.ToLower(dirs[j]["name"])
	})
	parent := filepath.Dir(abs)
	if parent == abs {
		parent = ""
	}

	jsonOut(w, map[string]any{"path": abs, "parent": parent, "dirs": dirs})
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

	supported := map[string]bool{".epub": true, ".fb2": true, ".cbz": true, ".pdf": true}
	books := make([]book, 0)
	warnings := make([]string, 0)
	paths := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, e os.DirEntry, walkErr error) error {
		if err := r.Context().Err(); err != nil {
			return err
		}

		if walkErr != nil {
			slog.Warn("cannot access library entry; skipping", "path", path, "error", walkErr)
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, walkErr))
			return nil
		}

		if e.IsDir() {
			if path != root && (!prefs.Recursive || (!prefs.IncludeHidden && strings.HasPrefix(e.Name(), "."))) {
				return filepath.SkipDir
			}
			return nil
		}

		if (!prefs.IncludeHidden && strings.HasPrefix(e.Name(), ".")) || e.Type()&os.ModeSymlink != 0 || !supported[strings.ToLower(filepath.Ext(path))] {
			return nil
		}

		info, err := e.Info()
		if err != nil {
			slog.Warn("cannot inspect library file; skipping", "path", path, "error", err)
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			return nil
		}

		slog.Debug("scanning book", "path", path)
		fileMeta, err := metadata.Read(path)
		if err != nil {
			slog.Warn("book metadata unreadable; using filename", "path", path, "error", err)
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			fileMeta = map[string]any{
				"title":      strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
				"authors":    []string{},
				"read_error": err.Error(),
			}
		}

		fingerprint := fingerprint(path, info, fileMeta)
		draft, staged, err := s.db.ReadBook(r.Context(), path, fingerprint, fileMeta)
		if err != nil {
			return err
		}

		id := idFor(path)
		paths[id] = path
		books = append(books, makeBook(path, draft, fileMeta, staged))
		return nil
	})
	if err != nil {
		jsonError(w, err)
		return
	}

	s.pathsMu.Lock()
	s.paths = paths
	s.root = root
	s.pathsMu.Unlock()
	sortBooks(books)
	slog.Info("library scan completed", "books", len(books), "warnings", len(warnings), "duration", time.Since(started))
	view := makeLibraryView(root, root, "", books)
	view.Warnings = warnings
	jsonOut(w, view)
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

func (s *Server) lookupPath(id string) string {
	s.pathsMu.RLock()
	defer s.pathsMu.RUnlock()
	return s.paths[id]
}
