package metadata

import (
	"os/exec"
	"syscall"
)

func configureCommand(cmd *exec.Cmd) {
	// Optional console tools such as Tesseract must remain invisible on GUI launches.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
