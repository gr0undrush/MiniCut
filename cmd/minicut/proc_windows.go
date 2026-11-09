//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// hideWindow stops a console window from flashing up for child processes.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// killTree ends a process AND everything it started. This matters when
// "ffmpeg.exe" in PATH is only a launcher (Chocolatey / Scoop shims): killing
// just the launcher would leave the real ffmpeg encoding in the background.
func killTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(p.Pid))
	hideWindow(kill)
	kill.Run()
	p.Kill()
	return nil
}
