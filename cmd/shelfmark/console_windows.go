package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	attachConsole         = kernel32.NewProc("AttachConsole")
	setConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// consoleOutput reuses the launching terminal, never allocating a new console.
// Retain inherited handles first: redirected output must keep going to its file
// or pipe even when AttachConsole replaces the Windows standard handles.
func consoleOutput() io.Writer {
	inherited := usableHandle(windows.STD_ERROR_HANDLE)
	if inherited == 0 {
		inherited = usableHandle(windows.STD_OUTPUT_HANDLE)
	}

	const attachParentProcess = 0xffffffff
	_, _, _ = attachConsole.Call(attachParentProcess)

	if inherited != 0 {
		return os.NewFile(uintptr(inherited), "console")
	}

	if handle := usableHandle(windows.STD_ERROR_HANDLE); handle != 0 {
		return os.NewFile(uintptr(handle), "console")
	}

	return nil
}

func usableHandle(kind uint32) windows.Handle {
	handle, err := windows.GetStdHandle(kind)
	if err != nil || handle == 0 || handle == windows.InvalidHandle {
		return 0
	}

	if _, err := windows.GetFileType(handle); err != nil {
		return 0
	}

	return handle
}

// AttachConsole resets handlers registered by the Go runtime before main.
// Install our own Ctrl+C/Ctrl+Break callback after attachment so terminal
// interrupts still cancel the server and let it shut down cleanly.
func interruptContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	ctx, stop := context.WithCancel(ctx)
	callback := syscall.NewCallback(func(event uint32) uintptr {
		if event == windows.CTRL_C_EVENT || event == windows.CTRL_BREAK_EVENT {
			stop()
			return 1
		}
		return 0
	})
	_, _, _ = setConsoleCtrlHandler.Call(callback, 1)

	return ctx, func() {
		_, _, _ = setConsoleCtrlHandler.Call(callback, 0)
		stop()
		cancel()
	}
}
