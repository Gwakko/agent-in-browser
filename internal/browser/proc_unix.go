//go:build !windows

package browser

import (
	"os/exec"
	"syscall"
)

// detach puts the browser in its own session so it survives the parent
// (e.g. an MCP server restarted by the agent).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// Alive reports whether a process with this PID exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
