package main

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// logLevel is shared with Settings so saved changes update the existing handler.
var logLevel slog.LevelVar

// diagnosticOutput keeps flag help visible even with an error-only log level.
var diagnosticOutput io.Writer = os.Stderr

// configureLogging appends diagnostics beside the executable, regardless of the
// working directory. The optional console also receives logs and flag output.
func configureLogging(programDir string, console io.Writer) (func(), error) {
	filename := filepath.Join(programDir, "shelfmark.log")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file %s: %w", filename, err)
	}

	// Fatal runtime failures bypass the logger. Keep an additional crash output
	// in the same file so a windowed launch still leaves useful diagnostics.
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("configure crash log: %w", err)
	}

	var output io.Writer = file
	if console != nil {
		output = io.MultiWriter(file, console)
	}
	// The helper writes raw native diagnostics while the server logs messages.
	// Serialize both paths so individual writes stay together in every sink.
	diagnosticOutput = &lockedWriter{output: output}
	slog.SetDefault(slog.New(slog.NewTextHandler(diagnosticOutput, &slog.HandlerOptions{
		Level: &logLevel,
	})))
	// Bridge legacy log.Print calls; HTTP errors have their own explicit level.
	slog.SetLogLoggerLevel(slog.LevelInfo)
	log.SetFlags(0)

	return func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		diagnosticOutput = io.Discard
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = file.Close()
	}, nil
}

type lockedWriter struct {
	mu     sync.Mutex
	output io.Writer
}

func (w *lockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.output.Write(data)
}
