//go:build !webview_helper

package main

import (
	"fmt"
	"os"
)

// main keeps the package linkable for plain "go build ./..." runs, which do not
// enable the cgo-based webview_helper tag required by the real helper.
func main() {
	fmt.Fprintln(os.Stderr, "shelfmark-window must be built with -tags webview_helper")
	os.Exit(1)
}
