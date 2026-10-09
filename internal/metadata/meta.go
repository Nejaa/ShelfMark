package metadata

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Read extracts metadata using the reader selected by the file extension.
// Unknown extensions return a filename-based title without inspecting content.
func Read(path string) (map[string]any, error) {
	slog.Debug("reading book metadata", "path", path, "format", filepath.Ext(path))
	base := map[string]any{"title": strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "authors": []string{}}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return readEPUBFile(path)
	case ".cbz":
		return readCBZFile(path)
	case ".fb2":
		return readFB2File(path)
	case ".pdf":
		return readPDFFile(path)
	default:
		return base, nil
	}
}

// Write validates a metadata patch and replaces the book through its format
// writer. Omitted fields are preserved; explicit empty values clear fields.
func Write(ctx context.Context, path string, fields map[string]any) error {
	slog.Debug("metadata writer selected", "path", path, "format", filepath.Ext(path), "fields", len(fields))
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateFields(path, fields); err != nil {
		return err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return writeEPUBFile(ctx, path, fields)
	case ".cbz":
		return writeCBZFile(ctx, path, fields)
	case ".fb2":
		return writeFB2File(path, fields)
	case ".pdf":
		return writePDFFile(ctx, path, fields)
	default:
		return fmt.Errorf("metadata writing is supported for EPUB, FB2, CBZ and PDF")
	}
}

// ReadText returns best-effort local clues from opening and closing content.
// OCR is optional; unreadable content yields an empty result.
func ReadText(ctx context.Context, path string, ocr bool) (clues string) {
	started := time.Now()
	slog.Debug("local content inspection started", "path", path, "ocr", ocr)
	defer func() {
		slog.Debug("local content inspection completed", "path", path, "clue_bytes", len(clues), "duration", time.Since(started), "canceled", ctx.Err() != nil)
	}()
	if ctx.Err() != nil {
		return ""
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return epubText(ctx, path, ocr)
	case ".cbz":
		return cbzText(ctx, path, ocr)
	case ".fb2":
		return fb2Text(path)
	case ".txt":
		b, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("local text could not be read", "path", path, "error", err)
		}
		return frontBack(string(b), 200000, 50000)
	case ".html", ".htm":
		b, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("local HTML could not be read", "path", path, "error", err)
		}
		return frontBack(htmlText(string(b)), 200000, 50000)
	case ".pdf":
		return pdfText(path)
	default:
		return ""
	}
}

// Cover returns an embedded EPUB or CBZ cover as a data URL, or an empty string
// when no supported cover is found.
func Cover(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var data []byte
	var mime string
	if ext == ".epub" || ext == ".cbz" {
		archive, err := zip.OpenReader(path)
		if err != nil {
			return "", err
		}

		defer func() { _ = archive.Close() }()
		if ext == ".epub" {
			data, mime = epubCover(archive.File)
		} else {
			data, mime = firstArchiveCover(archive.File)
		}
	}

	if len(data) == 0 {
		return "", nil
	}

	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
