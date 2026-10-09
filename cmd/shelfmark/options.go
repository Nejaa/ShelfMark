package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// options holds startup configuration; UI preferences live in the database.
type options struct {
	headless    bool
	listen      string
	dataDir     string
	logLevel    slog.Level
	logLevelSet bool
}

func parseOptions(programDir string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("shelfmark", flag.ContinueOnError)
	flags.SetOutput(diagnosticOutput)
	flags.BoolVar(&opts.headless, "headless", false, "serve the UI over HTTP without opening a desktop window")
	flags.StringVar(&opts.listen, "listen", "127.0.0.1:8766", "HTTP listen address")
	flags.StringVar(&opts.dataDir, "data-dir", programDir, "directory for Shelfmark's SQLite database")

	level := "info"
	flags.StringVar(&level, "log-level", "info", "startup log level override: debug, info, warn, error (otherwise use saved settings)")

	err := flags.Parse(os.Args[1:])
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "log-level" {
			opts.logLevelSet = true
		}
	})
	if opts.dataDir == "" {
		opts.dataDir = programDir
	}

	if err != nil {
		return opts, err
	}
	level = strings.ToLower(strings.TrimSpace(level))
	if err := opts.logLevel.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		return opts, fmt.Errorf("invalid log level %q: use debug, info, warn, or error", level)
	}
	// Accept the documented levels, rather than slog's numeric offsets.
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return opts, fmt.Errorf("invalid log level %q: use debug, info, warn, or error", level)
	}
	return opts, nil
}
