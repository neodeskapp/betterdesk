package servicedef

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/advancedconfig"
)

// commandTimeout is how long one command may take.
const commandTimeout = 5 * time.Second

// RunShell runs a command line with the system's shell (/bin/sh -c, on
// Windows cmd.exe /d /s /c) and returns its standard output. Its errors
// read like those of Node's execSync: "Command failed: " with the
// command and what it wrote to standard error, or "spawnSync /bin/sh
// ETIMEDOUT" after five seconds.
func RunShell(ctx context.Context, command string) (string, error) {
	shell, args := "/bin/sh", []string{"-c", command}
	if runtime.GOOS == "windows" {
		shell = os.Getenv("ComSpec")
		if shell == "" {
			shell = "cmd.exe"
		}
		args = []string{"/d", "/s", "/c", command}
	}
	out, err := run(ctx, shell, args, command)
	return out, err
}

// RunFile runs a program without a shell, with the errors of Node's
// execFileSync.
func RunFile(ctx context.Context, name string, args ...string) error {
	_, err := run(ctx, name, args, strings.Join(append([]string{name}, args...), " "))
	return err
}

func run(ctx context.Context, name string, args []string, shown string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return advancedconfig.DecodeUTF8(stdout.Bytes()), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", errors.New("spawnSync " + name + " ETIMEDOUT")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		message := "Command failed: " + shown
		if stderr.Len() > 0 {
			message += "\n" + advancedconfig.DecodeUTF8(stderr.Bytes())
		}
		return "", errors.New(message)
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("spawnSync " + name + " ENOENT")
	}
	return "", err
}

// fileError gives a failed read or write of path Node's message:
// "EACCES: permission denied, open '<path>'".
func fileError(err error, path string) error {
	code, text := "", ""
	switch {
	case errors.Is(err, fs.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case isEPERM(err):
		code, text = "EPERM", "operation not permitted"
	case errors.Is(err, fs.ErrPermission):
		code, text = "EACCES", "permission denied"
	case errors.Is(err, syscall.EISDIR):
		code, text = "EISDIR", "illegal operation on a directory"
	case errors.Is(err, syscall.EROFS):
		code, text = "EROFS", "read-only file system"
	default:
		return err
	}
	return &nodeFileError{msg: code + ": " + text + ", open '" + path + "'", err: err}
}

type nodeFileError struct {
	msg string
	err error
}

func (e *nodeFileError) Error() string { return e.msg }
func (e *nodeFileError) Unwrap() error { return e.err }

func isEPERM(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == syscall.EPERM
}

// cleanPath is path.resolve of an absolute path, through its links when
// it exists.
func cleanPath(path string) string {
	clean := filepath.Clean(path)
	if _, err := os.Stat(clean); err == nil {
		if real, err := filepath.EvalSymlinks(clean); err == nil {
			return real
		}
	}
	return clean
}
