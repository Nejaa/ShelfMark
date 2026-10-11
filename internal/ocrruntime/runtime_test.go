package ocrruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractWritesOnlyLocalRegularFiles(t *testing.T) {
	original := payload
	t.Cleanup(func() { payload = original })
	payload = ocrArchive(t, []archiveEntry{{name: "tessdata/eng.traineddata", data: []byte("model")}, {name: "tesseract", data: []byte("engine"), mode: 0700}})
	dir := t.TempDir()
	if err := extract(context.Background(), dir); err != nil {
		t.Fatalf("extract() error = %v", err)
	}
	for name, want := range map[string][]byte{"tessdata/eng.traineddata": []byte("model"), "tesseract": []byte("engine")} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read extracted %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	info, err := os.Stat(filepath.Join(dir, "tesseract"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("executable mode = %o, want 700", info.Mode().Perm())
	}
}

func TestExtractRejectsUnsafeArchiveEntries(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry archiveEntry
	}{
		{name: "path traversal", entry: archiveEntry{name: "../outside", data: []byte("no")}},
		{name: "symbolic link", entry: archiveEntry{name: "link", typeflag: tar.TypeSymlink}},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := payload
			t.Cleanup(func() { payload = original })
			payload = ocrArchive(t, []archiveEntry{test.entry})
			dir := t.TempDir()
			if err := extract(context.Background(), dir); err == nil {
				t.Fatal("extract() succeeded for an unsafe archive entry")
			}
		})
	}
}

func TestExtractHonorsCancellation(t *testing.T) {
	original := payload
	t.Cleanup(func() { payload = original })
	payload = ocrArchive(t, []archiveEntry{{name: "model", data: []byte("model")}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := extract(ctx, t.TempDir()); err != context.Canceled {
		t.Fatalf("extract() error = %v, want context.Canceled", err)
	}
}

func TestInstallModelPreservesExistingAndInstallsAtomically(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "bundled.traineddata")
	destination := filepath.Join(dir, "tessdata", "eng.traineddata")
	if err := os.WriteFile(source, []byte("bundled"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("user model"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installModel(context.Background(), source, destination); err != nil {
		t.Fatalf("installModel() existing: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != "user model" {
		t.Fatalf("existing model = %q, error %v; user model should be preserved", got, err)
	}

	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := installModel(context.Background(), source, destination); err != nil {
		t.Fatalf("installModel() missing model: %v", err)
	}
	got, err = os.ReadFile(destination)
	if err != nil || string(got) != "bundled" {
		t.Fatalf("installed model = %q, error %v", got, err)
	}

	if err := os.WriteFile(destination, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := installModel(context.Background(), source, destination); err == nil {
		t.Fatal("installModel() accepted an empty existing model")
	}
}

type archiveEntry struct {
	name     string
	data     []byte
	mode     int64
	typeflag byte
}

func ocrArchive(t *testing.T, entries []archiveEntry) []byte {
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
