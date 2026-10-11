//go:build !windows

package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// consoleOutput uses the current process stderr on platforms without AttachConsole.
func consoleOutput() io.Writer {
	return os.Stderr
}

// interruptContext relies on the default signal wiring outside Windows.
func interruptContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
