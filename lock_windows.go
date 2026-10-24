package main

import (
	"os"
	"unsafe"
)

var lockFileEx = kernel32.NewProc("LockFileEx")

const (
	lockfileExclusiveLock   = 2
	lockfileFailImmediately = 1
)

// lockFile - an exclusive lock on f, false if another process has it; gone when the process ends, also when it crashes
func lockFile(f *os.File) bool {
	var overlapped [32]byte // OVERLAPPED - offset 0
	r, _, _ := lockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(&overlapped[0])))
	return r != 0
}
