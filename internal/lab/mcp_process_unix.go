//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package lab

import (
	"errors"
	"os"
	osexec "os/exec"
	"syscall"
)

func configureMCPProcess(cmd *osexec.Cmd) {
	// Launchers such as npx start children. Cancellation must terminate the
	// private process group, not leave the actual MCP server running behind it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
