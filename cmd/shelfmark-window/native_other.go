//go:build webview_helper && !linux

package main

import webview "github.com/webview/webview_go"

func configureWindow(webview.WebView) {}

func canCreateWindow() bool {
	return true
}
