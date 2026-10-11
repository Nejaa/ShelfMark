//go:build webview_helper && !linux

package main

import webview "github.com/webview/webview_go"

// configureWindow leaves non-Linux platforms on the webview library defaults.
func configureWindow(webview.WebView) {
	// Other platforms use the webview library's native default window settings.
}

// canCreateWindow assumes non-Linux platforms will report window-creation failures directly.
func canCreateWindow() bool {
	return true
}
