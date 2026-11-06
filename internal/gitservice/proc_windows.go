//go:build windows

package gitservice

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts git in a new process group on Windows. Killing the
// direct process is best-effort; grandchildren started by remote helpers may
// outlive it, which is documented as a Windows limitation.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
