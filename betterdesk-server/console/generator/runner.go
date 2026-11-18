package generator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Command is one run of a tool: what child_process.spawn was given.
type Command struct {
	Name string
	Args []string
	// Dir is the working directory; "" is the console's.
	Dir string
	// Env is the whole environment; nil is the console's.
	Env []string
	// Timeout ends the tool after this long; 0 waits as long as it takes.
	Timeout time.Duration
	// Stdout, when set, gets the output as it comes, besides the result.
	Stdout io.Writer
}

// Result is how a tool ended. Code is its exit code, or -1 when a signal
// ended it (null in the old console's messages).
type Result struct {
	Code   int
	Stdout string
	Stderr string
	// TimedOut says Timeout ended the tool.
	TimedOut bool
}

// Runner starts tools. Run returns an error only when the tool could not
// be started; its text is the one Node gave ("spawn tar ENOENT").
type Runner interface {
	Run(ctx context.Context, c Command) (Result, error)
}

// ExecRunner runs tools with os/exec.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, c Command) (Result, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if c.Stdout != nil {
		cmd.Stdout = io.MultiWriter(&stdout, c.Stdout)
	}
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: decodeUTF8(stdout.Bytes()), Stderr: decodeUTF8(stderr.Bytes())}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
		if c.Timeout > 0 && ctx.Err() == context.DeadlineExceeded {
			res.TimedOut = true
		}
	default:
		return res, spawnError(c, err)
	}
	return res, nil
}

// spawnError words a tool that did not start the way Node did; its code
// is err.code.
func spawnError(c Command, err error) error {
	code := "ENOENT"
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
	case errors.Is(err, fs.ErrPermission):
		code = "EACCES"
	default:
		var errno syscall.Errno
		if errors.As(err, &errno) {
			code = errnoCode(errno)
		}
	}
	return &CodedError{Code: code, Message: "spawn " + c.Name + " " + code}
}

// codeText is a tool's exit code as Node printed it: the number, or null
// when a signal ended the tool.
func codeText(code int) string {
	if code < 0 {
		return "null"
	}
	return fmt.Sprint(code)
}

// nodeFSError words a failed file operation as Node's fs module did,
// "ENOENT: no such file or directory, open '/x'", with the errno name as
// err.code.
func nodeFSError(err error, syscallName, path string) error {
	var errno syscall.Errno
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	if !errors.As(err, &errno) {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return &CodedError{Code: "ENOENT", Message: fmt.Sprintf("ENOENT: no such file or directory, %s '%s'", syscallName, path)}
		case errors.Is(err, fs.ErrPermission):
			return &CodedError{Code: "EACCES", Message: fmt.Sprintf("EACCES: permission denied, %s '%s'", syscallName, path)}
		case errors.Is(err, fs.ErrExist):
			return &CodedError{Code: "EEXIST", Message: fmt.Sprintf("EEXIST: file already exists, %s '%s'", syscallName, path)}
		}
		return err
	}
	code := errnoCode(errno)
	return &CodedError{Code: code, Message: fmt.Sprintf("%s: %s, %s '%s'", code, errnoText(code, errno), syscallName, path)}
}

func errnoCode(errno syscall.Errno) string {
	if name, ok := errnoNames[errno]; ok {
		return name
	}
	return strings.ToUpper(strings.ReplaceAll(errno.Error(), " ", "_"))
}

func errnoText(code string, errno syscall.Errno) string {
	if text, ok := errnoTexts[code]; ok {
		return text
	}
	return errno.Error()
}

var errnoTexts = map[string]string{
	"ENOENT":    "no such file or directory",
	"EACCES":    "permission denied",
	"EEXIST":    "file already exists",
	"ENOTDIR":   "not a directory",
	"EISDIR":    "illegal operation on a directory",
	"ENOTEMPTY": "directory not empty",
	"EPERM":     "operation not permitted",
	"EXDEV":     "cross-device link not permitted",
	"ENOSPC":    "no space left on device",
	"EBUSY":     "resource busy or locked",
	"ELOOP":     "too many symbolic links encountered",
}

// decodeUTF8 is Buffer.toString('utf8'): every broken sequence becomes
// U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), string(utf8.RuneError))
}

// lastChars is text.slice(-n) for n UTF-16 code units.
func lastChars(text string, n int) string {
	if len(text) <= n {
		return text
	}
	us := units(text)
	if len(us) <= n {
		return text
	}
	return fromUnits(us[len(us)-n:])
}
