package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"shelfmark/internal/app"
	"shelfmark/internal/catalog"
	"shelfmark/internal/metadata"
	"shelfmark/internal/settings"
	"shelfmark/internal/store"
)

// main keeps os.Exit outside execute so deferred cleanup always runs.
func main() {
	os.Exit(execute())
}

// execute performs startup work and returns the process exit code.
func execute() int {
	console := consoleOutput()

	executable, err := os.Executable()
	if err != nil {
		if console != nil {
			_, _ = fmt.Fprintf(console, "Shelfmark: locate executable: %v\n", err)
		}
		return 1
	}

	programDir := filepath.Dir(executable)

	closeLog, err := configureLogging(programDir, console)
	if err != nil {
		if console != nil {
			_, _ = fmt.Fprintf(console, "Shelfmark: %v\n", err)
		}
		return 1
	}

	defer closeLog()

	if err := run(programDir); err != nil {
		slog.Error("Shelfmark stopped with an error", "error", err)
		return 1
	}

	return 0
}

// run opens the store, starts HTTP serving, and optionally hands control to the desktop UI.
func run(programDir string) error {
	options, err := parseOptions(programDir)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	if err != nil {
		return err
	}

	logLevel.Set(options.logLevel)
	defer slog.Info("Shelfmark stopped")

	ctx, stop := interruptContext(context.Background())
	defer stop()

	db, err := store.Open(ctx, filepath.Join(options.dataDir, "shelfmark.sqlite3"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("could not close database", "error", err)
		}
	}()

	prefs, err := settings.Load(ctx, db)
	if err != nil {
		return fmt.Errorf("load preferences: %w", err)
	}

	// Explicit flags select the initial level; Settings can change it live.
	// Without an override, restore the persisted threshold on every launch.
	if !options.logLevelSet {
		logLevel.Set(prefs.LogLevel)
	}

	slog.Info("starting Shelfmark", "data_dir", options.dataDir, "listen", options.listen,
		"headless", options.headless, "log_level", logLevel.Level().String())

	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", options.listen, err)
	}

	searcher := catalog.New()
	application := app.New(db, searcher, app.WithLogLevel(&logLevel), app.WithContext(ctx))
	server, serveErr, url := startHTTPServer(ctx, listener, application.Handler(), stop)

	// Graceful shutdown happens below; this guarantees listeners are released if a path ever skips it.
	defer func() { _ = server.Close() }() // best effort after graceful shutdown

	// OCR is independent of the GUI and must also be available in headless mode.
	// The server is already listening; a native-engine failure is recoverable.
	closeOCR, ocrErr := metadata.InitializeOCR(ctx, programDir)
	defer closeOCR()

	// Join background work before removing its native OCR runtime.
	defer application.Close()

	if ocrErr != nil {
		slog.Warn("could not prepare bundled OCR", "error", ocrErr,
			"hint", "check write permissions beside the executable and in the temporary directory")
	}

	if prefs.InspectContent && prefs.OCR {
		metadata.CheckOCR(ctx)
	} else {
		slog.Debug("OCR capability check skipped", "reason", "local OCR is disabled in settings")
	}

	opened, windowErr := tryDesktopWindow(ctx, options, programDir, url)
	if opened {
		stop()
		return shutdown(server)
	}

	if windowErr != nil && ctx.Err() == nil {
		slog.Warn("desktop window unavailable; serving HTTP only", "error", windowErr)
	}

	return serveUntilStopped(ctx, server, serveErr)
}

// startHTTPServer serves in the background and returns the bound loopback URL used by browsers and the helper.
func startHTTPServer(ctx context.Context, listener net.Listener, handler http.Handler, stop context.CancelFunc) (*http.Server, <-chan error, string) {
	server := &http.Server{
		Handler:  handler,
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		serveErr <- err
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}()

	address := listener.Addr().String()
	host, port, _ := net.SplitHostPort(address)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	url := "http://" + net.JoinHostPort(host, port)
	slog.Info("Shelfmark is ready (Ctrl+C to stop)", "url", url)

	return server, serveErr, url
}

// tryDesktopWindow decides whether the native helper should own the current session or leave the server in HTTP-only mode.
func tryDesktopWindow(ctx context.Context, options options, programDir, url string) (bool, error) {
	if options.headless {
		slog.Info("serving HTTP only", "reason", "headless mode requested")
		return false, nil
	}

	if !graphical() {
		slog.Warn("no graphical display detected; serving HTTP only", "hint", "use a browser, or launch in a desktop session with DISPLAY or WAYLAND_DISPLAY set")
		return false, nil
	}

	if err := desktopConsent(programDir, url); err != nil {
		return false, err
	}

	if err := openDesktop(ctx, url); err != nil {
		return false, err
	}

	return true, nil
}

// serveUntilStopped translates context cancellation and server exit into one shutdown path.
func serveUntilStopped(ctx context.Context, server *http.Server, serveErr <-chan error) error {
	select {
	case <-ctx.Done():
		if err := shutdown(server); err != nil {
			return err
		}

		err := <-serveErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}

		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return fmt.Errorf("serve HTTP: %w", err)
	}
}

// shutdown asks the HTTP server to drain active requests before closing listeners.
func shutdown(server *http.Server) error {
	slog.Info("shutting down HTTP server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shut down HTTP server: %w", err)
	}

	slog.Debug("HTTP shutdown completed")
	return nil
}

// graphical reports whether a native window has a reasonable chance of appearing on this platform.
func graphical() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}
