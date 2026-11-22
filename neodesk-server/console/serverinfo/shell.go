package serverinfo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// shellTimeout is how long the restart command may take.
const shellTimeout = 120 * time.Second

// runShell runs a command line with the system's shell in dir. Its errors
// read like those of Node's child_process.execSync, whose message the
// restart answer carried: "Command failed: " with the command and what it
// wrote to standard error, or "spawnSync /bin/sh ETIMEDOUT" after the
// time limit.
func runShell(ctx context.Context, dir, command string) error {
	return runShellWithin(ctx, dir, command, shellTimeout)
}

func runShellWithin(ctx context.Context, dir, command string, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	shell, args := "/bin/sh", []string{"-c", command}
	if runtime.GOOS == "windows" {
		shell = os.Getenv("ComSpec")
		if shell == "" {
			shell = "cmd.exe"
		}
		args = []string{"/d", "/s", "/c", command}
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("spawnSync " + shell + " ETIMEDOUT")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		message := "Command failed: " + command
		if stderr.Len() > 0 {
			message += "\n" + decodeUTF8(stderr.Bytes())
		}
		return errors.New(message)
	}
	var fileErr *FileError
	if errors.As(nodeError(err, "spawnSync", shell), &fileErr) && fileErr.Code != "" {
		return errors.New("spawnSync " + shell + " " + fileErr.Code)
	}
	return err
}
