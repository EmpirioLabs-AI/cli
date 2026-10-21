//go:build !windows

package app

import "os"

func removeSelf(path string) error { return os.Remove(path) }

// The installer only prints a PATH hint on macOS and Linux, so there is nothing to undo.
func removeFromPath(string) {}
