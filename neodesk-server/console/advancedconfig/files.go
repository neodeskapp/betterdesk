package advancedconfig

import (
	"errors"
	"io/fs"
	"strings"
	"syscall"
	"unicode/utf8"
)

// FileError is a failed file operation with the message Node gives it,
// "EACCES: permission denied, open '/etc/x'": the settings routes answer
// with it. The cause is still there for errors.Is.
type FileError struct {
	msg string
	err error
}

func (e *FileError) Error() string { return e.msg }
func (e *FileError) Unwrap() error { return e.err }

// nodeCode is Node's name and text for the cause of a file error.
func nodeCode(err error) (code, text string) {
	var errno syscall.Errno
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT", "no such file or directory"
	case errors.As(err, &errno) && errno == syscall.EPERM:
		return "EPERM", "operation not permitted"
	case errors.Is(err, fs.ErrPermission):
		return "EACCES", "permission denied"
	case errors.Is(err, syscall.EISDIR):
		return "EISDIR", "illegal operation on a directory"
	case errors.Is(err, syscall.ENOTDIR):
		return "ENOTDIR", "not a directory"
	case errors.Is(err, syscall.EROFS):
		return "EROFS", "read-only file system"
	case errors.Is(err, syscall.ENOSPC):
		return "ENOSPC", "no space left on device"
	case errors.Is(err, syscall.ENAMETOOLONG):
		return "ENAMETOOLONG", "name too long"
	case errors.Is(err, syscall.ELOOP):
		return "ELOOP", "too many symbolic links encountered"
	}
	return "", ""
}

// nodeError words the error of a file operation: call is the system call
// Node names, path what it puts after it.
func nodeError(err error, call, path string) error {
	code, text := nodeCode(err)
	if code == "" {
		return &FileError{msg: err.Error(), err: err}
	}
	return &FileError{msg: code + ": " + text + ", " + call + " '" + path + "'", err: err}
}

// nodeCopyError words the error of fs.copyFile, which names both files.
func nodeCopyError(err error, from, to string) error {
	code, text := nodeCode(err)
	if code == "" {
		return &FileError{msg: err.Error(), err: err}
	}
	return &FileError{msg: code + ": " + text + ", copyfile '" + from + "' -> '" + to + "'", err: err}
}

// DecodeUTF8 is Buffer.toString('utf8'): each broken sequence becomes one
// replacement character, as the WHATWG decoder does it.
func DecodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	needed, seen := 0, 0
	var cp rune
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c < 0x80:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				out.WriteRune(utf8.RuneError)
			}
			continue
		}
		if c < lower || c > upper {
			needed, seen, cp = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(utf8.RuneError)
			i--
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			out.WriteRune(cp)
			needed, seen, cp = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

// Text is the content of a file in a JSON answer, written the way
// JSON.stringify writes a string: \b and \f as such, other control
// characters as \u00XX, everything else as it is.
type Text string

const hexDigits = "0123456789abcdef"

// MarshalJSON writes the text as JSON.stringify would.
func (t Text) MarshalJSON() ([]byte, error) {
	s := string(t)
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			out = append(out, '\\', '"')
		case c == '\\':
			out = append(out, '\\', '\\')
		case c == '\b':
			out = append(out, '\\', 'b')
		case c == '\f':
			out = append(out, '\\', 'f')
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\r':
			out = append(out, '\\', 'r')
		case c == '\t':
			out = append(out, '\\', 't')
		case c < 0x20:
			out = append(out, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		default:
			out = append(out, c)
		}
	}
	return append(out, '"'), nil
}
