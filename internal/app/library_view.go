package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"shelfmark/internal/library"
)

type folderView struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Depth int    `json:"depth"`
}

type libraryView struct {
	Root        string       `json:"root"`
	Folder      string       `json:"folder"`
	Books       []book       `json:"books"`
	Folders     []folderView `json:"folders"`
	FolderCount int          `json:"folder_count"`
	Total       int          `json:"total"`
	Warnings    []string     `json:"warnings"`
}

func makeBook(path string, working, current map[string]any, staged bool) book {
	return book{ID: idFor(path), Path: path, Name: filepath.Base(path), Metadata: working,
		FileMetadata: current, Staged: staged, SearchQuery: library.SearchQuery(working, filepath.Base(path)),
		CoverEditable: strings.EqualFold(filepath.Ext(path), ".epub")}
}

func sortBooks(books []book) {
	order := library.NewOrdering()
	sort.Slice(books, func(i, j int) bool {
		a, b := books[i], books[j]
		return order.Compare(a.Metadata, b.Metadata, a.Name, b.Name, a.Path, b.Path) < 0
	})
}

// withinFolder uses server OS path rules; clients never interpret server paths.
func withinFolder(path, folder string) bool {
	relative, err := filepath.Rel(folder, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func makeLibraryView(root, folder, filter string, books []book) libraryView {
	view := libraryView{Root: root, Folder: folder, Books: []book{}, Folders: []folderView{}, Total: len(books), Warnings: []string{}}
	dirs := map[string]bool{}
	if root != "" {
		dirs[root] = true
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	for _, b := range books {
		for dir := filepath.Dir(b.Path); withinFolder(dir, root); dir = filepath.Dir(dir) {
			dirs[dir] = true
			if dir == root {
				break
			}
		}
		if !withinFolder(b.Path, folder) {
			continue
		}
		view.FolderCount++
		haystack := strings.ToLower(b.Name + " " + library.Text(b.Metadata["title"]) + " " + library.Text(b.Metadata["authors"]))
		if filter == "" || strings.Contains(haystack, filter) {
			view.Books = append(view.Books, b)
		}
	}
	sortBooks(view.Books)
	for dir := range dirs {
		relative, _ := filepath.Rel(root, dir)
		depth := 0
		if relative != "." {
			depth = len(strings.Split(relative, string(filepath.Separator)))
		}
		view.Folders = append(view.Folders, folderView{Path: dir, Name: filepath.ToSlash(relative), Depth: depth})
	}
	sort.Slice(view.Folders, func(i, j int) bool { return view.Folders[i].Name < view.Folders[j].Name })
	return view
}

// scannedBooks supplies the current working metadata without rescanning files.
func (s *Server) scannedBooks(ctx context.Context, folder, filter string) (libraryView, error) {
	s.operations.RLock()
	defer s.operations.RUnlock()
	s.pathsMu.RLock()
	root := s.root
	paths := make([]string, 0, len(s.paths))
	for _, path := range s.paths {
		paths = append(paths, path)
	}
	s.pathsMu.RUnlock()
	if root == "" {
		return libraryView{}, errors.New("scan a library folder first")
	}
	if folder == "" {
		folder = root
	}
	folder = filepath.Clean(folder)
	if !withinFolder(folder, root) {
		return libraryView{}, errors.New("folder is outside the current scan")
	}
	books := make([]book, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return libraryView{}, err
		}
		state, err := s.db.State(ctx, path)
		if err != nil {
			return libraryView{}, err
		}
		books = append(books, makeBook(path, library.Working(state.Metadata, state.Staged), state.Metadata, len(state.Staged) > 0))
	}
	return makeLibraryView(root, folder, filter, books), nil
}

func (s *Server) libraryView(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Folder string `json:"folder"`
		Filter string `json:"filter"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}
	view, err := s.scannedBooks(r.Context(), in.Folder, in.Filter)
	if err != nil {
		jsonError(w, err)
		return
	}
	jsonOut(w, view)
}
