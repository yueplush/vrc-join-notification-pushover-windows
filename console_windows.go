//go:build windows && (!cgo || arm64)

package main

import (
	"os"
	"syscall"
)

const attachParentProcess = ^uint32(0)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole    = kernel32.NewProc("AttachConsole")
	procAllocConsole     = kernel32.NewProc("AllocConsole")
	procSetConsoleCtrl   = kernel32.NewProc("SetConsoleCtrlHandler")
	procFreeConsole      = kernel32.NewProc("FreeConsole")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
)

// ensureConsole attaches to a parent console or allocates a new one when the
// fallback CLI is built as a GUI executable.
func ensureConsole() {
	if hasConsole() {
		return
	}
	if !attachConsole() && !allocConsole() {
		return
	}
	redirectStdHandles()
}

func hasConsole() bool {
	if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
		return true
	}
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return false
	}
	return handle != 0 && handle != syscall.InvalidHandle
}

func attachConsole() bool {
	r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess))
	return r != 0
}

func allocConsole() bool {
	r, _, _ := procAllocConsole.Call()
	return r != 0
}

func redirectStdHandles() {
	// Disable the default CTRL handlers so closing the console does not
	// terminate the process unexpectedly.
	procSetConsoleCtrl.Call(0, 1)

	if h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		if f := os.NewFile(uintptr(h), "CONIN$"); f != nil {
			os.Stdin = f
		}
	}
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		if f := os.NewFile(uintptr(h), "CONOUT$"); f != nil {
			os.Stdout = f
		}
	}
	if h, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		if f := os.NewFile(uintptr(h), "CONOUT$"); f != nil {
			os.Stderr = f
		}
	}
}

func releaseConsole() {
	procFreeConsole.Call()
}
