//go:build !windows

package app

import (
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

func exitCode(err *exec.ExitError) int {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return err.ExitCode()
}

// forwarder passes SIGTERM and SIGHUP, which reach only empirio, to the agent.
type forwarder struct {
	ch   chan os.Signal
	mu   sync.Mutex
	proc *os.Process
	done chan struct{}
}

func forwardSignals() *forwarder {
	f := &forwarder{ch: make(chan os.Signal, 4), done: make(chan struct{})}
	// Ctrl+\ reaches the agent from the terminal, like Ctrl+C.
	signal.Ignore(syscall.SIGQUIT)
	signal.Notify(f.ch, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for {
			select {
			case s := <-f.ch:
				f.mu.Lock()
				if f.proc != nil {
					_ = f.proc.Signal(s)
				}
				f.mu.Unlock()
			case <-f.done:
				return
			}
		}
	}()
	return f
}

func (f *forwarder) attach(p *os.Process) {
	f.mu.Lock()
	f.proc = p
	f.mu.Unlock()
}

func (f *forwarder) stop() {
	signal.Stop(f.ch)
	signal.Reset(syscall.SIGQUIT)
	close(f.done)
}
