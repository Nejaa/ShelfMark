//go:build !windows

package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func consoleOutput() io.Writer {
	return os.Stderr
}

func interruptContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
