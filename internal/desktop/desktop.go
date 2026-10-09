// Package desktop launches an isolated, embedded native window process.
package desktop

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const readyMessage = "SHELFMARK_WINDOW_READY"

// Run blocks until the window closes. Startup failures and crashes are errors;
// callers can keep their HTTP server running independently of the native GUI.
func Run(ctx context.Context, url string) error {
	if len(payload) == 0 {
		return errors.New("this build has no embedded desktop runtime")
	}

	// Every launch gets a private runtime so builds cannot share loaded DLLs.
	slog.Debug("extracting embedded desktop runtime", "compressed_bytes", len(payload))
	dir, err := os.MkdirTemp("", "sm-")
	if err != nil {
		return fmt.Errorf("create desktop directory: %w", err)
	}

	defer removeTemporaryDirectory(dir)
	if err := extract(dir); err != nil {
		return fmt.Errorf("extract desktop runtime: %w", err)
	}

	if err := relocate(dir); err != nil {
		return fmt.Errorf("relocate desktop runtime: %w", err)
	}

	if err := prepareRuntime(dir); err != nil {
		return fmt.Errorf("prepare desktop runtime: %w", err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	name := "shelfmark-window"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	cmd := exec.CommandContext(childCtx, filepath.Join(dir, name), url)
	cmd.Dir = dir
	cmd.Env = environment(dir)
	if runtime.GOOS == "windows" {
		// Isolate the browser profile from other builds and running windows.
		// Keep it outside the runtime directory's AppContainer code ACLs.
		profile, err := os.MkdirTemp("", "sm-data-")
		if err != nil {
			return fmt.Errorf("create browser profile: %w", err)
		}

		defer removeTemporaryDirectory(profile)
		cmd.Env = append(cmd.Env, "WEBVIEW2_USER_DATA_FOLDER="+profile)
	}

	defer cleanupProcess(cmd)
	cmd.Stderr = diagnosticWriter{}
	configureProcess(cmd)
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	defer func() {
		_ = stdin.Close()
	}()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	slog.Debug("starting desktop helper", "runtime_dir", dir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start desktop helper: %w", err)
	}

	// Readiness is sent by the SPA after initialization, not just process start.
	// Keep draining stdout after readiness so native diagnostics cannot block it.
	ready := make(chan struct{})
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		scanner := bufio.NewScanner(stdout)
		announced := false
		for scanner.Scan() {
			line := scanner.Text()
			if line == readyMessage && !announced {
				close(ready)
				announced = true
			} else {
				slog.Debug("desktop helper output", "message", line)
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		<-scanned
		done <- err
	}()
	// Only GUI startup is bounded; a healthy window may stay open indefinitely.
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	select {
	case <-ready:
		slog.Info("Shelfmark desktop window opened")
	case err := <-done:
		if err == nil {
			err = errors.New("helper exited without opening a window")
		}
		return fmt.Errorf("desktop startup: %w", err)
	case <-timer.C:
		cancel()
		<-done
		return errors.New("desktop startup timed out")
	case <-ctx.Done():
		cancel()
		<-done
		return ctx.Err()
	}

	if err := <-done; err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("desktop helper exited unexpectedly: %w", err)
	}

	slog.Info("desktop window closed")
	return nil
}

func removeTemporaryDirectory(dir string) {
	// WebView2 subprocesses can briefly retain DLL and profile handles after
	// their helper exits. Windows cannot unlink those files until they close.
	deadline := time.Now()
	if runtime.GOOS == "windows" {
		deadline = deadline.Add(5 * time.Second)
	}
	for {
		err := os.RemoveAll(dir)
		if err == nil {
			return
		}

		if !time.Now().Before(deadline) {
			slog.Warn("could not remove temporary desktop directory", "path", dir, "error", err)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// extract accepts only relative regular files and directories. Symlinks and
// duplicate files are rejected, preventing a payload from escaping its root.
func extract(dir string) error {
	zipped, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return err
	}

	defer func() {
		_ = zipped.Close()
	}()
	archive := tar.NewReader(zipped)
	var total int64
	for {
		entry, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		if !filepath.IsLocal(entry.Name) {
			return fmt.Errorf("invalid archive path %q", entry.Name)
		}

		path := filepath.Join(dir, entry.Name)
		if entry.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}

		if entry.Typeflag != tar.TypeReg {
			return fmt.Errorf("unsupported archive entry %q", entry.Name)
		}

		total += entry.Size
		if entry.Size < 0 || total > 3<<30 {
			return errors.New("desktop archive is too large")
		}

		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}

		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(file, archive)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}

		if entry.Mode&0111 != 0 {
			if err := os.Chmod(path, 0700); err != nil {
				return err
			}
		}
	}
}

// Linux distribution builds compile absolute WebKit subprocess paths into ELF
// strings. Replace complete NUL-terminated strings without changing ELF offsets.
func relocate(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "relocations.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	var patches []struct{ File, Old, New string }
	if err := json.Unmarshal(raw, &patches); err != nil {
		return err
	}

	for _, patch := range patches {
		if !filepath.IsLocal(patch.File) {
			return errors.New("invalid relocation path")
		}

		path := filepath.Join(dir, patch.File)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		replacement := strings.ReplaceAll(patch.New, "${ROOT}", dir)
		if len(replacement) > len(patch.Old) {
			return errors.New("temporary directory path is too long for WebKit; use a shorter TMPDIR")
		}

		old := append([]byte(patch.Old), 0)
		if !bytes.Contains(content, old) {
			return fmt.Errorf("missing relocation string in %s", patch.File)
		}

		padded := make([]byte, len(old))
		copy(padded, replacement)
		content = bytes.ReplaceAll(content, old, padded)
		if err := os.WriteFile(path, content, 0600); err != nil {
			return err
		}
	}

	return nil
}

// environment directs the helper to its private libraries and data files.
// Linux uses bundled software rendering to avoid requiring host GPU drivers.
func environment(dir string) []string {
	env := os.Environ()
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(filepath.Join(dir, "webview2")); err == nil {
			env = append(env, "WEBVIEW2_BROWSER_EXECUTABLE_FOLDER="+filepath.Join(dir, "webview2"))
		}
		return env
	}

	if runtime.GOOS != "linux" {
		return env
	}

	values := map[string]string{
		"LD_LIBRARY_PATH":                 filepath.Join(dir, "lib"),
		"GSETTINGS_SCHEMA_DIR":            filepath.Join(dir, "share", "glib-2.0", "schemas"),
		"GIO_EXTRA_MODULES":               filepath.Join(dir, "gio"),
		"GIO_MODULE_DIR":                  filepath.Join(dir, "gio"),
		"GDK_PIXBUF_MODULE_FILE":          filepath.Join(dir, "pixbuf", "loaders.cache"),
		"GDK_PIXBUF_MODULEDIR":            filepath.Join(dir, "pixbuf"),
		"XKB_CONFIG_ROOT":                 filepath.Join(dir, "share", "X11", "xkb"),
		"FONTCONFIG_FILE":                 filepath.Join(dir, "fonts.conf"),
		"LIBGL_ALWAYS_SOFTWARE":           "1",
		"LIBGL_DRIVERS_PATH":              filepath.Join(dir, "dri"),
		"__EGL_VENDOR_LIBRARY_FILENAMES":  filepath.Join(dir, "egl-mesa.json"),
		"WEBKIT_DISABLE_COMPOSITING_MODE": "1",
		"GTK_THEME":                       "Adwaita:dark",
		"XDG_DATA_DIRS":                   filepath.Join(dir, "share") + ":" + os.Getenv("XDG_DATA_DIRS") + ":/usr/local/share:/usr/share",
	}
	// WSLg exposes both backends, but this bundled WebKit/GTK runtime can
	// execute the page without painting it on Wayland. Prefer XWayland there;
	// GTK can still try Wayland if X11 is unavailable. Respect an explicit
	// backend choice and leave other Linux desktops' selection unchanged.
	if os.Getenv("GDK_BACKEND") == "" && os.Getenv("DISPLAY") != "" &&
		(os.Getenv("WSL2_GUI_APPS_ENABLED") == "1" || os.Getenv("WSL_DISTRO_NAME") != "") {
		values["GDK_BACKEND"] = "x11,wayland"
	}

	for key, value := range values {
		filtered := env[:0]
		for _, entry := range env {
			if !strings.HasPrefix(entry, key+"=") {
				filtered = append(filtered, entry)
			}
		}
		env = append(filtered, key+"="+value)
	}

	return env
}

// diagnosticWriter keeps native stderr visible at warning level. Stdout is a
// separate protocol and must not be mixed with these diagnostic messages.
type diagnosticWriter struct{}

func (diagnosticWriter) Write(data []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			slog.Warn("desktop runtime diagnostic", "message", line)
		}
	}
	return len(data), nil
}
