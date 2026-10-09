package desktop

import (
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	// Even a helper built with the console subsystem must not allocate one.
	// This flag does not prevent the native webview from opening its GUI window.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

func cleanupProcess(_ *exec.Cmd) {}
