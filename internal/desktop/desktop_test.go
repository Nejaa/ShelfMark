package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCreatesPrivateFilesAndDirectories(t *testing.T) {
	original := payload
	t.Cleanup(func() { payload = original })
	payload = desktopArchive(t, []desktopEntry{{name: "runtime/bin/helper", data: []byte("helper"), mode: 0700}, {name: "runtime/data/file", data: []byte("data")}})
	dir := t.TempDir()
	if err := extract(dir); err != nil {
		t.Fatalf("extract() error = %v", err)
	}
	for name, want := range map[string]string{"runtime/bin/helper": "helper", "runtime/data/file": "data"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	info, err := os.Stat(filepath.Join(dir, "runtime/bin/helper"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("helper permissions = %o, want 700", info.Mode().Perm())
	}
}

func TestExtractRejectsTraversalLinksAndDuplicates(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []desktopEntry
	}{
		{name: "path traversal", entries: []desktopEntry{{name: "../outside", data: []byte("escape")}}},
		{name: "symbolic link", entries: []desktopEntry{{name: "link", typeflag: tar.TypeSymlink}}},
		{name: "duplicate member", entries: []desktopEntry{{name: "same", data: []byte("first")}, {name: "same", data: []byte("second")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := payload
			t.Cleanup(func() { payload = original })
			payload = desktopArchive(t, test.entries)
			if err := extract(t.TempDir()); err == nil {
				t.Fatal("extract() succeeded for invalid archive")
			}
		})
	}
}

type desktopEntry struct {
	name     string
	data     []byte
	mode     int64
	typeflag byte
}

func desktopArchive(t *testing.T, entries []desktopEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), Typeflag: typeflag}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := archive.Write(entry.data); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buffer.Bytes()
}
