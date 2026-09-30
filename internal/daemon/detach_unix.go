//go:build unix

package daemon

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon in its own session so it outlives the hook
// process and is not killed with Codex's process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
