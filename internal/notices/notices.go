// Package notices exposes the original license texts bundled with a release.
package notices

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// Archive returns the complete notices archive. Plain go builds have no archive;
// build-target.sh collects notices from the actual packaged dependencies.
func Archive() []byte { return payload }

// Read retrieves a notice without extracting the archive onto the filesystem.
func Read(name string) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open notices: %w", err)
	}
	defer func() { _ = reader.Close() }() // Closing an in-memory reader cannot fail.
	archive := tar.NewReader(reader)
	for {
		entry, err := archive.Next()
		if err != nil {
			return nil, fmt.Errorf("read notice %q: %w", name, err)
		}
		if entry.Name == name && entry.Typeflag == tar.TypeReg {
			if entry.Size > 2<<20 {
				return nil, fmt.Errorf("notice %q exceeds size limit", name)
			}
			return io.ReadAll(archive)
		}
	}
}
