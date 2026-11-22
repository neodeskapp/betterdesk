//go:build !windows

package system

import (
	"context"
	"os/exec"
	"time"
)

// runShell runs a command line with /bin/sh -c and returns its standard
// output. Standard error is dropped.
func runShell(ctx context.Context, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	return string(out), err
}
