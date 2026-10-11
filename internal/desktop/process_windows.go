package desktop

import (
	"os/exec"
	"syscall"
)

// configureProcess suppresses an inherited console window when the helper executable is built as a console app.
func configureProcess(cmd *exec.Cmd) {
	// Even a helper built with the console subsystem must not allocate one.
	// This flag does not prevent the native webview from opening its GUI window.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// cleanupProcess relies on Windows Cmd.Cancel and Wait handling rather than extra best-effort signals.
func cleanupProcess(_ *exec.Cmd) {
	// Windows helper processes are terminated through Cmd.Cancel and reaped by Wait.
}
