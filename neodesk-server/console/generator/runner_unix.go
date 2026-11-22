//go:build !windows

package generator

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// killTreeOnCancel starts the tool in a process group of its own and
// kills the whole group when the context ends, so the compilers and
// downloads a build script starts end with it.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
