//go:build webview_helper

// The native GUI lives in a separate process so loader failures and crashes do
// not affect the HTTP server. This executable is embedded by the desktop build.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"

	webview "github.com/webview/webview_go"
)

func main() {
	// GUI creation, event handling, and destruction must use the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected the Shelfmark UI URL")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if !canCreateWindow() {
		fmt.Fprintln(os.Stderr, "no usable graphical display")
		os.Exit(1)
	}

	window := webview.New(false)
	if window == nil {
		fmt.Fprintln(os.Stderr, "could not create a webview")
		os.Exit(1)
	}

	defer window.Destroy()
	configureWindow(window)
	window.SetTitle("Shelfmark — Ebook Metadata")
	window.SetSize(1400, 950, webview.HintNone)

	// Stdout is the parent readiness protocol; stderr is for native diagnostics.
	var ready sync.Once
	if err := window.Bind("shelfmarkWindowReady", func() {
		ready.Do(func() {
			fmt.Fprintln(os.Stdout, "SHELFMARK_WINDOW_READY")
		})
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	window.Navigate(os.Args[1])

	// The stdin pipe is a parent-liveness signal, not an input channel.
	// EOF also shuts down the window if the server is killed unexpectedly.
	eof := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(eof)
	}()

	done := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)

	// Dispatch termination onto the GUI thread. On Windows, Terminate posts a
	// quit message to its calling thread; calling it here directly would hang.
	go func() {
		defer watcher.Done()
		select {
		case <-ctx.Done():
			window.Dispatch(func() {
				window.Terminate()
			})
		case <-eof:
			window.Dispatch(func() {
				window.Terminate()
			})
		case <-done:
		}
	}()

	// Wait for the watcher before Destroy can release the native handle.
	window.Run()
	close(done)
	watcher.Wait()
}
