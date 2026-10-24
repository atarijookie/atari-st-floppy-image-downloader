//go:build !windows

package main

import (
	"os"
	"syscall"
)

// lockFile - an exclusive lock on f, false if another process has it; gone when the process ends, also when it crashes
func lockFile(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
