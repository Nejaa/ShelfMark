//go:build !linux && !windows

package desktop

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}

func cleanupProcess(cmd *exec.Cmd) {}
