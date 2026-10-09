//go:build !windows

package desktop

func prepareRuntime(_ string) error {
	return nil
}
