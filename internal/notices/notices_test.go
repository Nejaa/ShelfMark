package notices

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestReadNoticeFromArchive(t *testing.T) {
	original := payload
	t.Cleanup(func() { payload = original })
	payload = noticeArchive(t, map[string]string{"LICENSE.txt": "license text", "THIRD-PARTY.txt": "dependency notices"})

	got, err := Read("LICENSE.txt")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(got) != "license text" {
		t.Fatalf("Read() = %q, want license text", got)
	}
	if _, err := Read("missing.txt"); err == nil {
		t.Fatal("Read() succeeded for a missing notice")
	}
	if !bytes.Equal(Archive(), payload) {
		t.Fatal("Archive() does not return the bundled archive")
	}
}

func noticeArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for name, content := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := io.WriteString(archive, content); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close tar archive: %v", err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("close gzip archive: %v", err)
	}
	return buffer.Bytes()
}
