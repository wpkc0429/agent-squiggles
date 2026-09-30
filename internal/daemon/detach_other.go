//go:build !unix

package daemon

import "os/exec"

func detach(cmd *exec.Cmd) {}
