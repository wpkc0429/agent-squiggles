//go:build !unix

package lsp

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
