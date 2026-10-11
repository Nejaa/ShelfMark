//go:build !windows

package desktop

// prepareRuntime is only needed on Windows, where the extracted WebView2 files need extra ACLs.
func prepareRuntime(_ string) error {
	return nil
}
