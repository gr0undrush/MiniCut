package main

import (
	"context"
	"os/exec"
	"time"
)

// newCmd is exec.CommandContext with a hidden console window, and a cancel
// that kills the whole process tree. WaitDelay makes Wait give up on pipes
// that an orphaned child might still hold open.
func newCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second
	return cmd
}
