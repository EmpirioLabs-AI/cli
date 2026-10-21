//go:build windows

package app

import (
	"os"
	"os/exec"
)

func exitCode(err *exec.ExitError) int { return err.ExitCode() }

// On Windows the console delivers Ctrl+C and Ctrl+Break to every process
// attached to it, so there is nothing to forward.
type forwarder struct{}

func forwardSignals() *forwarder        { return &forwarder{} }
func (f *forwarder) attach(*os.Process) {}
func (f *forwarder) stop()              {}
