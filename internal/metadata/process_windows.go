package metadata

import (
	"os/exec"
	"syscall"
)

// configureCommand hides helper process windows during GUI-triggered OCR work.
func configureCommand(cmd *exec.Cmd) {
	// Optional console tools such as Tesseract must remain invisible on GUI launches.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
