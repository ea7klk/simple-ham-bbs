//go:build unix

package main

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on file, blocking until it is
// available. It is used to serialise log rotation across processes.
func lockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

// unlockFile releases a lock taken by lockFile.
func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
