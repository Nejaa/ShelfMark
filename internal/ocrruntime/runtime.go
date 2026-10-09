// Package ocrruntime prepares the private engine and language data embedded in
// packaged Shelfmark builds. It has no dependency on a graphical environment.
package ocrruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

// Runtime locates the extracted executable and persistent language models.
type Runtime struct {
	Executable string
	DataDir    string
	directory  string
}

// Bundled reports whether this build includes a private OCR engine.
func Bundled() bool { return len(payload) != 0 }

// Prepare extracts the engine into a private directory and installs the default
// models beside Shelfmark. Existing language files are preserved, including
// models the user has supplied. The server can continue if preparation fails.
func Prepare(ctx context.Context, programDir string) (*Runtime, error) {
	if !Bundled() {
		return nil, errors.New("this build has no bundled OCR runtime")
	}
	dir, err := os.MkdirTemp("", "smo-")
	if err != nil {
		return nil, fmt.Errorf("create OCR runtime directory: %w", err)
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(dir)
		}
	}()

	slog.Debug("extracting bundled OCR runtime", "compressed_bytes", len(payload))
	if err := extract(ctx, dir); err != nil {
		return nil, err
	}
	dataDir := filepath.Join(programDir, "tessdata")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create language directory: %w", err)
	}
	for _, language := range []string{"eng", "fra"} {
		name := language + ".traineddata"
		if err := installModel(ctx, filepath.Join(dir, "tessdata", name), filepath.Join(dataDir, name)); err != nil {
			return nil, err
		}
	}
	name := "tesseract"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	prepared = true
	slog.Info("bundled OCR runtime prepared", "language_dir", dataDir)
	return &Runtime{Executable: filepath.Join(dir, name), DataDir: dataDir, directory: dir}, nil
}

// Close removes private native code only; downloaded or installed language data
// remains beside the executable for reuse on the next launch.
func (r *Runtime) Close() {
	if err := os.RemoveAll(r.directory); err != nil {
		slog.Warn("could not remove OCR runtime directory", "path", r.directory, "error", err)
	}
}

func extract(ctx context.Context, dir string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer func() { _ = compressed.Close() }()
	archive := tar.NewReader(compressed)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(entry.Name) || entry.Typeflag != tar.TypeReg {
			return fmt.Errorf("invalid OCR archive entry %q", entry.Name)
		}
		total += entry.Size
		if entry.Size < 0 || total > 128<<20 {
			return errors.New("OCR runtime archive exceeds its size limit")
		}
		path := filepath.Join(dir, entry.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if entry.Mode&0111 != 0 {
			mode = 0700
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, archive)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
	}
}

// installModel completes each file before publishing it. A failed copy leaves
// existing data untouched and never publishes a partial traineddata file.
func installModel(ctx context.Context, source, destination string) error {
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("language model %s is not a nonempty regular file; remove it to restore the bundled model", destination)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.CreateTemp(filepath.Dir(destination), ".traineddata-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(output.Name()) }()
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	if err := errors.Join(copyErr, syncErr, output.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(output.Name(), destination); err != nil {
		return err
	}
	slog.Info("installed bundled OCR language model", "path", destination)
	return nil
}
