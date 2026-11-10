//go:build !unix

package main

import "os"

// lockFile is a no-op on platforms without flock. The container image is Linux,
// so this only affects local development on other systems, where rotation is
// single-process anyway.
func lockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }
