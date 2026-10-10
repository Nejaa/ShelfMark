//go:build !windows

package main

// Microsoft runtime terms apply only to the Windows desktop implementation.
func desktopConsent(programDir, url string) error { return nil }
