package filetransfer

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
)

// The old service took whatever JSON a device or a request sent and let
// JavaScript make numbers, texts and bytes of it. The functions here do
// the same for parsed JSON values (goapi.Parse): nil, bool, float64,
// string, []any and *goapi.Object.

// jsText is String(v). ok is false where JavaScript throws: an object
// with a member named toString.
func jsText(v any) (text string, ok bool) {
	switch t := v.(type) {
	case undefinedValue:
		return "undefined", true
	case nil:
		return "null", true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case float64:
		return goapipath.NumberText(t), true
	case string:
		return t, true
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item == nil {
				continue
			}
			part, ok := jsText(item)
			if !ok {
				return "", false
			}
			parts[i] = part
		}
		return strings.Join(parts, ","), true
	case *goapi.Object:
		if t.Has("toString") {
			return "", false
		}
		return "[object Object]", true
	}
	return "", false
}

var decimalLiteral = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*(?:[eE][+-]?[0-9]+)?|\.[0-9]+(?:[eE][+-]?[0-9]+)?)$`)

// toNumber is Number(v): 0 for null, false and blank text, NaN for text
// that is no number and for objects. ok is false where JavaScript throws.
func toNumber(v any) (n float64, ok bool) {
	switch t := v.(type) {
	case undefinedValue:
		return math.NaN(), true
	case nil:
		return 0, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		return t, true
	case string:
		return textToNumber(t), true
	}
	text, ok := jsText(v)
	if !ok {
		return 0, false
	}
	return textToNumber(text), true
}

// number is toNumber for a value that was checked before: the size of a
// transfer that exists.
func number(v any) float64 {
	n, _ := toNumber(v)
	return n
}

func textToNumber(text string) float64 {
	s := strings.TrimFunc(text, isJSSpace)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			value := 0.0
			for i := 2; i < len(s); i++ {
				digit, err := strconv.ParseUint(s[i:i+1], base, 8)
				if err != nil {
					return math.NaN()
				}
				value = value*float64(base) + float64(digit)
			}
			return value
		}
	}
	if !decimalLiteral.MatchString(s) {
		return math.NaN()
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(value, 0) {
		return math.NaN()
	}
	return value
}

// ParseSize is the size a request names, as the routes for people read
// it: parseInt(size, 10) || 0. A whole number at the start of the value's
// text counts ("12abc" is 12, 262144.5 is 262144); anything without one is
// 0. The error is ErrNotText.
func ParseSize(size any) (float64, error) {
	text, ok := jsText(size)
	if !ok {
		return 0, ErrNotText
	}
	// With a radix of 10, "0x10" is the 0 in front of the x
	digits := strings.TrimLeft(strings.TrimLeftFunc(text, isJSSpace), "+-")
	if len(digits) >= 2 && digits[0] == '0' && (digits[1] == 'x' || digits[1] == 'X') {
		return 0, nil
	}
	n := goapipath.ParseInt(text)
	if math.IsNaN(n) || n == 0 {
		return 0, nil
	}
	return n, nil
}

// isJSSpace reports the white space JavaScript skips around a number.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// errChunkTooLarge refuses a chunk of more than MaxChunkSize bytes.
var errChunkTooLarge = fmt.Errorf("Chunk too large (max %d bytes)", MaxChunkSize)

const notBufferLike = "The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object."

// bufferFrom is Buffer.from(data, 'base64') for the "data" of a chunk
// message: base64 text, or a list of byte values, or the JSON form of a
// buffer ({type: "Buffer", data: [...]}), or an object with a length and
// numbered members, refused above MaxChunkSize before any memory is taken.
// Anything else is the error JavaScript gave.
func bufferFrom(data any) ([]byte, error) {
	switch t := data.(type) {
	case string:
		return decodeBase64(t), nil
	case []any:
		return bytesOf(t), nil
	case undefinedValue:
		return nil, errors.New(notBufferLike + " Received undefined")
	case nil:
		return nil, errors.New(notBufferLike + " Received null")
	case bool:
		return nil, errors.New(notBufferLike + " Received type boolean (" + strconv.FormatBool(t) + ")")
	case float64:
		return nil, errors.New(notBufferLike + " Received type number (" + goapipath.NumberText(t) + ")")
	case *goapi.Object:
		if t.Has("length") {
			length, isNumber := t.Get("length").(float64)
			if !isNumber || math.IsNaN(length) || length <= 0 {
				return []byte{}, nil
			}
			if length > MaxChunkSize {
				return nil, errChunkTooLarge
			}
			out := make([]byte, int(length))
			for i := range out {
				out[i] = toByte(t.Get(strconv.Itoa(i)))
			}
			return out, nil
		}
		if list, isList := t.Get("data").([]any); isList && t.Get("type") == "Buffer" {
			return bytesOf(list), nil
		}
		if t.Has("valueOf") {
			return nil, errors.New("value.valueOf is not a function")
		}
		return nil, errors.New(notBufferLike + " Received an instance of Object")
	}
	return nil, errors.New(notBufferLike)
}

// bytesOf is the bytes of a list of values: each as a number, cut to a
// whole number and taken modulo 256; what is no number is 0.
func bytesOf(list []any) []byte {
	out := make([]byte, len(list))
	for i, item := range list {
		out[i] = toByte(item)
	}
	return out
}

func toByte(v any) byte {
	n, ok := toNumber(v)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return byte(int64(math.Mod(math.Trunc(n), 256)))
}

// decodeBase64 reads base64 the forgiving way Node does: both alphabets
// (+/ and -_), characters outside them skipped, and the first "=" ends
// the text. Two characters left at the end give one byte, three give two.
// Node looks at the low byte of each UTF-16 code unit only, so a
// character above U+00FF counts as the Latin-1 character with its low
// byte: U+0141 reads as "A", and U+013D as the "=" that ends the text.
func decodeBase64(text string) []byte {
	out := make([]byte, 0, len(text)*3/4)
	var bits uint32
	n := 0
	for _, unit := range utf16.Encode([]rune(text)) {
		c := byte(unit)
		var v uint32
		switch {
		case c >= 'A' && c <= 'Z':
			v = uint32(c - 'A')
		case c >= 'a' && c <= 'z':
			v = uint32(c-'a') + 26
		case c >= '0' && c <= '9':
			v = uint32(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			return finishBase64(out, bits, n)
		default:
			continue
		}
		bits = bits<<6 | v
		n++
		if n == 4 {
			out = append(out, byte(bits>>16), byte(bits>>8), byte(bits))
			bits, n = 0, 0
		}
	}
	return finishBase64(out, bits, n)
}

// finishBase64 adds the bytes of the last, incomplete group of
// characters.
func finishBase64(out []byte, bits uint32, n int) []byte {
	switch n {
	case 2:
		out = append(out, byte(bits>>4))
	case 3:
		out = append(out, byte(bits>>10), byte(bits>>2))
	}
	return out
}

// baseName is path.basename(name) of Node: the last part of a path,
// without slashes at the end. On Windows a backslash separates too and a
// drive letter in front does not count.
func baseName(name string) string {
	windows := runtime.GOOS == "windows"
	isSeparator := func(c byte) bool { return c == '/' || (windows && c == '\\') }
	first := 0
	if windows && len(name) >= 2 && name[1] == ':' && (name[0]|0x20) >= 'a' && (name[0]|0x20) <= 'z' {
		first = 2
	}
	start, end, matchedSlash := first, -1, true
	for i := len(name) - 1; i >= first; i-- {
		if isSeparator(name[i]) {
			if !matchedSlash {
				start = i + 1
				break
			}
		} else if end == -1 {
			matchedSlash = false
			end = i + 1
		}
	}
	if end == -1 {
		return ""
	}
	return name[start:end]
}

// nodeFileError gives a file error the text Node gave it, which a device
// gets as the error of its chunk: "ENOENT: no such file or directory,
// open '<path>'".
func nodeFileError(err error, call, path string) error {
	var errno syscall.Errno
	code, text := "", ""
	switch {
	case errors.As(err, &errno) && errno == syscall.EISDIR:
		code, text = "EISDIR", "illegal operation on a directory"
	case errors.As(err, &errno) && errno == syscall.ENOTDIR:
		code, text = "ENOTDIR", "not a directory"
	case errors.As(err, &errno) && errno == syscall.ENOSPC:
		code, text = "ENOSPC", "no space left on device"
	case errors.As(err, &errno) && errno == syscall.ENAMETOOLONG:
		code, text = "ENAMETOOLONG", "name too long"
	case errors.Is(err, fs.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case errors.Is(err, fs.ErrPermission):
		code, text = "EACCES", "permission denied"
	default:
		return err
	}
	return errors.New(code + ": " + text + ", " + call + " '" + path + "'")
}
