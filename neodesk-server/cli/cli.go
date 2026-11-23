// Package cli holds the commands of neodesk-server besides running the
// server that the installers and images call (spec updater-installers.md
// 4.9, DECISIONS A33): upgrade-env, env merge, reset-password, config
// check and config set, and check. Each replaces a Node script of the old
// console and does what it did for the same input: the same files written
// byte for byte, the same exit status and, where a script printed JSON or
// a result line, the same text. Where a command takes its input another
// way (the password of reset-password on standard input), its doc says
// so.
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// Env is what a command uses besides files: its streams, the environment,
// the clock and the platform. Tests replace them.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Getenv reads the environment.
	Getenv func(string) string
	// Now is the clock.
	Now func() time.Time
	// GOOS is the platform the command acts for.
	GOOS string
}

// OS is the process's own Env.
func OS() Env {
	return Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Now: time.Now, GOOS: runtime.GOOS}
}

// object is a JavaScript object built by assignments, with own string
// keys: obj[key] = value. Its keys list as Object.keys lists them: the
// array indexes first, in numeric order, then the other keys in the order
// they were first set. "__proto__" is never an own key (assigning it sets
// the prototype or nothing).
type object[V any] struct {
	keys   []string
	values map[string]V
}

func newObject[V any]() *object[V] { return &object[V]{values: map[string]V{}} }

func (o *object[V]) set(key string, value V) {
	if key == "__proto__" {
		return
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// entries lists the keys in JavaScript's order.
func (o *object[V]) entries() []string {
	keys := append([]string(nil), o.keys...)
	sort.SliceStable(keys, func(a, b int) bool {
		ia, aIndex := arrayIndex(keys[a])
		ib, bIndex := arrayIndex(keys[b])
		switch {
		case aIndex && bIndex:
			return ia < ib
		case aIndex != bIndex:
			return aIndex
		}
		return false
	})
	return keys
}

// arrayIndex reports whether key is an array index as JavaScript writes
// one: "0", or digits without a leading zero, below 2^32-1.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= 1<<32-1 {
		return 0, false
	}
	return n, true
}

// orderedJSON is an object for writeJSON whose keys keep their order.
type orderedJSON struct {
	keys   []string
	values map[string]any
}

func (o orderedJSON) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := marshalPlain(key)
		if err != nil {
			return nil, err
		}
		v, err := marshalPlain(o.values[key])
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalPlain is json.Marshal without the escapes of "<", ">" and "&".
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

// jsonText is JSON.stringify(v), or JSON.stringify(v, null, 2) with
// indent. encoding/json writes "<", ">" and "&" as they are here, as
// JavaScript does; it escapes U+2028, U+2029 and bytes that are no UTF-8,
// which JavaScript writes as the characters (the bytes as U+FFFD), so
// those escapes are undone.
func jsonText(v any, indent bool) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return unescapeLikeJS(strings.TrimSuffix(b.String(), "\n")), nil
}

// unescapeLikeJS turns the escapes of U+2028, U+2029 and U+FFFD that
// encoding/json writes back into the characters.
func unescapeLikeJS(s string) string {
	if !strings.Contains(s, `\u`) {
		return s
	}
	var b strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inString = !inString
		case inString && c == '\\' && i+1 < len(s):
			if s[i+1] == 'u' && i+5 < len(s) {
				if r, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil && (r == 0x2028 || r == 0x2029 || r == 0xfffd) {
					b.WriteRune(rune(r))
					i += 5
					continue
				}
			}
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// parseIntPrefix is parseInt(s, 10): ok is false where it gives NaN.
func parseIntPrefix(s string) (int64, bool) {
	s = strings.TrimLeftFunc(s, jsbind.IsSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	var n int64
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		if n < 1<<53 {
			n = n*10 + int64(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if negative {
		n = -n
	}
	return n, true
}
