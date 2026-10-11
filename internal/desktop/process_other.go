//go:build !linux && !windows

package desktop

import "os/exec"

// configureProcess leaves process management to os/exec on platforms with no extra requirements.
func configureProcess(cmd *exec.Cmd) {
	// Other platforms use os/exec's default process configuration.
}

// cleanupProcess is a no-op on platforms without helper process-group handling.
func cleanupProcess(cmd *exec.Cmd) {
	// Other platforms need no extra process-group cleanup.
}
