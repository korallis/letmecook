//go:build linux || darwin

package repositories

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Cancel the owned Git process group, including transport/pack children. Never
// use process-name matching or signal an inherited supervisor process group.
func containCommand(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
