package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	getConsoleMode             = kernel32.NewProc("GetConsoleMode")
	setConsoleMode             = kernel32.NewProc("SetConsoleMode")
	getConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const enableVirtualTerminalProcessing = 0x0004

type consoleScreenBufferInfo struct {
	size, cursor                   [2]int16
	attributes                     uint16
	left, top, right, bottom       int16
	maxWindowSizeX, maxWindowSizeY int16
}

func consoleWidth(h syscall.Handle) (int, bool) {
	var info consoleScreenBufferInfo
	r, _, _ := getConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, false
	}
	return int(info.right-info.left) + 1, true
}

// stdoutTerminal - stdout is a console which understands the escape codes (Windows 10+, after switching them on),
// and its width
func stdoutTerminal() (bool, func() int) {
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if r, _, _ := getConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false, nil // redirected
	}
	if r, _, _ := setConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing)); r == 0 {
		return false, nil // older Windows - plain lines only
	}
	return true, func() int {
		w, ok := consoleWidth(h)
		if !ok {
			return 80
		}
		return w
	}
}
