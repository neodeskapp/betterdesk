//go:build windows

package generator

import (
	"os/exec"
	"strconv"
)

// killTreeOnCancel ends the tool and every process it started when the
// context ends, with taskkill /T; when taskkill cannot run, the tool
// alone is killed.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
