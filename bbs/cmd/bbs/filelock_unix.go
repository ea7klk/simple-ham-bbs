//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on file, blocking until it is
// available. It is used to serialise log rotation across processes.
func lockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

// tryLockFile takes an exclusive advisory lock without blocking, and reports
// whether it got it. EWOULDBLOCK means somebody else holds it, which is a normal
// answer, not an error.
func tryLockFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	}
	return false, err
}

// unlockFile releases a lock taken by lockFile.
func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
