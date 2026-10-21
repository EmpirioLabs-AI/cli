//go:build !windows

package config

import "os"

func replace(from, to string) error { return os.Rename(from, to) }
