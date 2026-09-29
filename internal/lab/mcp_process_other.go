//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package lab

import osexec "os/exec"

// CommandContext and the SDK close/reap the directly launched process.
func configureMCPProcess(cmd *osexec.Cmd) {}
