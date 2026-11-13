//go:build !unix

package main

import "os"

// lockFile is a no-op on platforms without flock. The container image is Linux,
// so this only affects local development on other systems, where rotation is
// single-process anyway.
func lockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }

// tryLockFile reports success on platforms without flock: there is only one
// process to contend with locally.
func tryLockFile(*os.File) (bool, error) { return true, nil }
