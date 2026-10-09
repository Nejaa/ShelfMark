package metadata

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/beevik/etree"
)

var replaceMu sync.Mutex

// createTempFile keeps writes on the same filesystem as the destination.
func createTempFile(filename string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(filename), "."+filepath.Base(filename)+".shelfmark-*")
}

// createTempPath reserves a unique name for libraries that create output files
// themselves. The handle is closed and the placeholder removed before use.
func createTempPath(filename string) (string, error) {
	file, err := createTempFile(filename)
	if err != nil {
		return "", err
	}

	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}

	if err := os.Remove(name); err != nil {
		return "", err
	}

	return name, nil
}

func base64Decode(value string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(value)
}

// replaceFile preserves permissions and flushes the completed replacement.
// Rename-over-existing is attempted first. If the platform refuses it, move the
// original aside, install the replacement, and restore the original on failure.
// The fallback is recoverable but has a brief gap in which the path is absent.
func replaceFile(temp, filename string) error {
	slog.Debug("replacing book file", "path", filename)
	replaceMu.Lock()
	defer replaceMu.Unlock()
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}

	if err := os.Chmod(temp, info.Mode().Perm()); err != nil {
		return err
	}

	file, err := os.OpenFile(temp, os.O_RDWR, 0)
	if err != nil {
		return err
	}

	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}

	if err := os.Rename(temp, filename); err == nil {
		return nil
	}

	slog.Debug("direct replacement unavailable; using backup and rollback", "path", filename)
	backup, err := createTempPath(filename)
	if err != nil {
		return err
	}

	if err := os.Rename(filename, backup); err != nil {
		return err
	}

	if err := os.Rename(temp, filename); err != nil {
		rollbackErr := os.Rename(backup, filename)
		return errors.Join(fmt.Errorf("replace %s: %w", filename, err), rollbackErr)
	}

	return os.Remove(backup)
}

// writeXMLAtomic finishes and closes the temporary XML file before replacement.
func writeXMLAtomic(filename string, doc *etree.Document) error {
	file, err := createTempFile(filename)
	if err != nil {
		return err
	}

	temp := file.Name()
	// A failed write leaves the original untouched; removal is best-effort cleanup.
	defer func() { _ = os.Remove(temp) }()
	doc.Indent(2)
	if _, err = doc.WriteTo(file); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}

	if err = file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}

	return replaceFile(temp, filename)
}
