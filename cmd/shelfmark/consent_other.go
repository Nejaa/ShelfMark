//go:build !windows

package main

// desktopConsent is a no-op outside Windows, where Shelfmark does not embed WebView2.
// Microsoft runtime terms apply only to the Windows desktop implementation.
func desktopConsent(programDir, url string) error { return nil }
