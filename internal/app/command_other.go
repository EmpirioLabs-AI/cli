//go:build !windows

package app

import (
	"context"
	"os/exec"
)

// agentCommand returns the command that runs binary with args.
func agentCommand(ctx context.Context, binary string, args []string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, binary, args...), nil
}
