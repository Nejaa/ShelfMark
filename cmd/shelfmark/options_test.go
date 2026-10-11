package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"testing"

	"log/slog"
)

func TestParseOptions(t *testing.T) {
	originalArgs := os.Args
	originalOutput := diagnosticOutput
	t.Cleanup(func() {
		os.Args = originalArgs
		diagnosticOutput = originalOutput
	})
	diagnosticOutput = io.Discard

	for _, test := range []struct {
		name     string
		args     []string
		want     options
		wantErr  bool
		wantHelp bool
	}{
		{
			name: "defaults",
			want: options{listen: "127.0.0.1:8766", dataDir: "/app", logLevel: slog.LevelInfo},
		},
		{
			name: "explicit options normalize level",
			args: []string{"-headless", "-listen", "localhost:9000", "-data-dir", "/data", "-log-level", " WARN "},
			want: options{headless: true, listen: "localhost:9000", dataDir: "/data", logLevel: slog.LevelWarn, logLevelSet: true},
		},
		{
			name: "empty data directory uses executable directory",
			args: []string{"-data-dir="},
			want: options{listen: "127.0.0.1:8766", dataDir: "/app", logLevel: slog.LevelInfo},
		},
		{name: "invalid level", args: []string{"-log-level", "trace"}, wantErr: true},
		{name: "unknown option", args: []string{"-unknown"}, wantErr: true},
		{name: "help", args: []string{"-h"}, wantHelp: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			os.Args = append([]string{"shelfmark"}, test.args...)
			got, err := parseOptions("/app")
			if test.wantHelp {
				if !errors.Is(err, flag.ErrHelp) {
					t.Fatalf("parseOptions() error = %v, want flag.ErrHelp", err)
				}
				return
			}
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseOptions() = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOptions() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseOptions() = %+v, want %+v", got, test.want)
			}
		})
	}
}
