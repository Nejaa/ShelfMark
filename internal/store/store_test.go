package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"shelfmark/internal/library"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "shelfmark.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return db
}

func TestReadBookOverlaysAndReconcilesDraft(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)
	path := "/books/title.epub"
	current := map[string]any{"title": "Original", "authors": []string{"Author"}}
	working, staged, err := db.ReadBook(ctx, path, "fingerprint-one", current)
	if err != nil {
		t.Fatalf("initial ReadBook() error = %v", err)
	}
	if staged || working["title"] != "Original" {
		t.Fatalf("initial ReadBook() = %#v, staged %t", working, staged)
	}

	draft := library.Working(current, map[string]any{"title": "Edited"})
	if err := db.Stage(ctx, path, "fingerprint-one", current, draft); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	working, staged, err = db.ReadBook(ctx, path, "fingerprint-two", current)
	if err != nil {
		t.Fatalf("ReadBook() with draft error = %v", err)
	}
	if !staged || working["title"] != "Edited" {
		t.Fatalf("ReadBook() failed to overlay draft: %#v, staged %t", working, staged)
	}

	current["title"] = "Edited"
	working, staged, err = db.ReadBook(ctx, path, "fingerprint-three", current)
	if err != nil {
		t.Fatalf("ReadBook() reconciliation error = %v", err)
	}
	if staged || working["title"] != "Edited" {
		t.Fatalf("already-applied draft was not reconciled: %#v, staged %t", working, staged)
	}
}

func TestFinishSaveRetainsOnlyUnselectedChanges(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)
	file := map[string]any{"title": "File", "authors": []string{"Author"}}
	draft := library.Working(file, map[string]any{"title": "Saved", "publisher": "Publisher"})
	if err := db.Stage(ctx, "/books/book.epub", "before", file, draft); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	written := library.Working(file, map[string]any{"title": "Saved"})
	remaining := map[string]any{"publisher": "Publisher"}
	if err := db.FinishSave(ctx, "/books/book.epub", "after", written, remaining); err != nil {
		t.Fatalf("FinishSave() error = %v", err)
	}
	state, err := db.State(ctx, "/books/book.epub")
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Fingerprint != "after" || state.Metadata["title"] != "Saved" || state.Staged["publisher"] != "Publisher" {
		t.Fatalf("saved state = %+v", state)
	}
}

func TestDiscardDraftsClearsSelectedPatches(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)
	for _, path := range []string{"/books/a.epub", "/books/b.epub"} {
		metadata := map[string]any{"title": "Original"}
		draft := map[string]any{"title": "Edited"}
		if err := db.Stage(ctx, path, "fingerprint", metadata, draft); err != nil {
			t.Fatalf("Stage(%q) error = %v", path, err)
		}
	}
	if err := db.DiscardDrafts(ctx, []string{"/books/a.epub"}); err != nil {
		t.Fatalf("DiscardDrafts() error = %v", err)
	}
	staged, err := db.Staged(ctx)
	if err != nil {
		t.Fatalf("Staged() error = %v", err)
	}
	if len(staged) != 1 || staged[0].Path != "/books/b.epub" {
		t.Fatalf("remaining staged books = %+v", staged)
	}
}

func TestCacheExpirationAndBound(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t)
	type result struct{ Value string }
	if err := db.CachePut(ctx, "fresh", result{Value: "fresh"}); err != nil {
		t.Fatalf("CachePut() error = %v", err)
	}
	var got result
	ok, err := db.CacheGet(ctx, "fresh", time.Hour, &got)
	if err != nil || !ok || got.Value != "fresh" {
		t.Fatalf("fresh cache get = (%+v, %t, %v)", got, ok, err)
	}
	ok, err = db.CacheGet(ctx, "fresh", 0, &got)
	if err != nil || ok {
		t.Fatalf("disabled cache get = (%+v, %t, %v), want miss", got, ok, err)
	}
	for i := range 505 {
		key, value, err := cacheEntry(i)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.CachePut(ctx, key, value); err != nil {
			t.Fatalf("CachePut(%s): %v", key, err)
		}
	}
	var count int
	if err := db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_matches").Scan(&count); err != nil {
		t.Fatalf("count cache rows: %v", err)
	}
	if count > 500 {
		t.Fatalf("cache has %d rows, want at most 500", count)
	}
}

func cacheEntry(i int) (string, map[string]int, error) {
	encoded, err := json.Marshal(map[string]int{"value": i})
	return string(encoded), map[string]int{"value": i}, err
}

func TestMigrationConvertsLegacyFullDraftsToPatch(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "legacy.sqlite3")
	raw, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	_, err = raw.Exec(`
		CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
		CREATE TABLE search_matches(cache_key TEXT PRIMARY KEY,result_json TEXT NOT NULL,updated_at INTEGER NOT NULL);
		CREATE TABLE book_state(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,metadata_json TEXT NOT NULL,staged_json TEXT,updated_at INTEGER NOT NULL);
		PRAGMA user_version = 1;
		INSERT INTO book_state(path,fingerprint,metadata_json,staged_json,updated_at)
		VALUES('/books/book.epub','old','{"title":"Old","authors":["A"]}','{"title":"New","authors":["A"],"subtitle":"Sub"}',1);
	`)
	if err != nil {
		t.Fatalf("create legacy database: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(context.Background(), filename)
	if err != nil {
		t.Fatalf("Open() migration error = %v", err)
	}
	defer func() { _ = db.Close() }()
	state, err := db.State(context.Background(), "/books/book.epub")
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if len(state.Staged) != 2 || state.Staged["title"] != "New" || state.Staged["subtitle"] != "Sub" {
		t.Fatalf("migrated patch = %#v", state.Staged)
	}
	var version int
	if err := db.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
}

func TestMigrationFailureLeavesLegacyVersion(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "broken.sqlite3")
	raw, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
		CREATE TABLE search_matches(cache_key TEXT PRIMARY KEY,result_json TEXT NOT NULL,updated_at INTEGER NOT NULL);
		CREATE TABLE book_state(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,metadata_json TEXT NOT NULL,staged_json TEXT,updated_at INTEGER NOT NULL);
		PRAGMA user_version = 1;
		INSERT INTO book_state(path,fingerprint,metadata_json,staged_json,updated_at)
		VALUES('/books/broken.epub','old','not json','{}',1);
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err := Open(context.Background(), filename); err == nil {
		t.Fatal("Open() unexpectedly migrated malformed row")
	}
	check, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	var version int
	if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("failed migration set schema version %d, want 1", version)
	}
}

func TestStateMissingBook(t *testing.T) {
	_, err := openTestStore(t).State(context.Background(), "/missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("State() error = %v, want sql.ErrNoRows", err)
	}
}
