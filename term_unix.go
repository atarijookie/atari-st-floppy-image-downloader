//go:build !windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

type winsize struct{ rows, cols, x, y uint16 }

func termSize(f *os.File) (int, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return int(ws.cols), errno == 0 && ws.cols > 0
}

// stdoutTerminal - stdout is a terminal (not redirected), and its width
func stdoutTerminal() (bool, func() int) {
	_, ok := termSize(os.Stdout)
	return ok, func() int {
		w, ok := termSize(os.Stdout)
		if !ok {
			return 80
		}
		return w
	}
}
