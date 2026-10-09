//go:build !windows

package metadata

import "os/exec"

func configureCommand(_ *exec.Cmd) {}
