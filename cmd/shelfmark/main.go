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
	server := &http.Server{
		Handler:  application.Handler(),
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Serve before starting the helper: GUI failure must leave HTTP usable.
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

	if !options.headless && graphical() {
		windowErr := openDesktop(ctx, url)
		// A successful call returns only after the user closes the window.
		if windowErr == nil {
			stop()
			return shutdown(server)
		}

		if ctx.Err() == nil {
			slog.Warn("desktop window unavailable; serving HTTP only", "error", windowErr)
		}
	} else if options.headless {
		slog.Info("serving HTTP only", "reason", "headless mode requested")
	} else {
		slog.Warn("no graphical display detected; serving HTTP only", "hint", "use a browser, or launch in a desktop session with DISPLAY or WAYLAND_DISPLAY set")
	}

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

func graphical() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}
