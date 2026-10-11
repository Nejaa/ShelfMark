//go:build !windows

package metadata

import "os/exec"

// configureCommand leaves non-Windows OCR processes with default attributes.
func configureCommand(_ *exec.Cmd) {
	// Only Windows needs process attributes, such as hiding console windows.
}
