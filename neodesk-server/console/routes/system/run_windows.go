package system

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// runShell runs a command line with cmd.exe /d /s /c and returns its
// standard output. Standard error is dropped.
func runShell(ctx context.Context, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, shell)
	// cmd.exe reads its command line itself: hand it over unquoted
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: shell + ` /d /s /c "` + command + `"`}
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	return string(out), err
}
