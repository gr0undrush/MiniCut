//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// hideWindow puts the child in its own process group so killTree can end it
// together with anything it started.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	syscall.Kill(-p.Pid, syscall.SIGKILL) // whole group
	p.Kill()
	return nil
}
