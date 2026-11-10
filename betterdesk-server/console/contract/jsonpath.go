package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A path names a place in a JSON body, written the way goldens and cases
// write it:
//
//	data.items[2].id        key, index, key
//	data.items[*].id        the id of every element
//	data.items[#]           the number of elements of data.items
//	data["odd key"].x       a key that is not a plain word
//	$                       the body itself
type path []step

type stepKind int

const (
	stepKey stepKind = iota
	stepIndex
	stepEvery
	stepLength
)

type step struct {
	kind  stepKind
	key   string
	index int
}

// rootPath is how the body itself is written.
const rootPath = "$"

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func (p path) String() string {
	if len(p) == 0 {
		return rootPath
	}
	var b strings.Builder
	for i, s := range p {
		switch s.kind {
		case stepKey:
			if plainKey.MatchString(s.key) {
				if i > 0 {
					b.WriteByte('.')
				}
				b.WriteString(s.key)
			} else {
				quoted, _ := json.Marshal(s.key)
				b.WriteByte('[')
				b.Write(quoted)
				b.WriteByte(']')
			}
		case stepIndex:
			b.WriteString("[" + strconv.Itoa(s.index) + "]")
		case stepEvery:
			b.WriteString("[*]")
		case stepLength:
			b.WriteString("[#]")
		}
	}
	return b.String()
}

// with returns p with one more step, without sharing memory with p.
func (p path) with(s step) path {
	out := make(path, len(p)+1)
	copy(out, p)
	out[len(p)] = s
	return out
}

func parsePath(text string) (path, error) {
	if text == rootPath {
		return path{}, nil
	}
	if text == "" {
		return nil, fmt.Errorf("empty path (the body itself is %s)", rootPath)
	}
	var p path
	rest := text
	for rest != "" {
		switch {
		case rest[0] == '[':
			end := strings.IndexByte(rest, ']')
			if strings.HasPrefix(rest, `["`) {
				// A quoted key may hold a bracket: find the closing quote first.
				dec := json.NewDecoder(strings.NewReader(rest[1:]))
				var key string
				if err := dec.Decode(&key); err != nil {
					return nil, fmt.Errorf("path %q: bad quoted key", text)
				}
				end = 1 + int(dec.InputOffset())
				if end >= len(rest) || rest[end] != ']' {
					return nil, fmt.Errorf("path %q: quoted key without ]", text)
				}
				p = append(p, step{kind: stepKey, key: key})
				rest = rest[end+1:]
				break
			}
			if end < 0 {
				return nil, fmt.Errorf("path %q: [ without ]", text)
			}
			inner := rest[1:end]
			switch inner {
			case "*":
				p = append(p, step{kind: stepEvery})
			case "#":
				p = append(p, step{kind: stepLength})
			default:
				index, err := strconv.Atoi(inner)
				if err != nil || index < 0 {
					return nil, fmt.Errorf("path %q: [%s] is not an index, * or #", text, inner)
				}
				p = append(p, step{kind: stepIndex, index: index})
			}
			rest = rest[end+1:]
		default:
			if rest[0] == '.' {
				if len(p) == 0 {
					return nil, fmt.Errorf("path %q starts with a dot", text)
				}
				rest = rest[1:]
			} else if len(p) > 0 {
				return nil, fmt.Errorf("path %q: expected . or [ before %q", text, rest)
			}
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			if end == 0 {
				return nil, fmt.Errorf("path %q has an empty key", text)
			}
			p = append(p, step{kind: stepKey, key: rest[:end]})
			rest = rest[end:]
		}
	}
	for i, s := range p {
		if s.kind == stepLength && i != len(p)-1 {
			return nil, fmt.Errorf("path %q: [#] must be the last step", text)
		}
	}
	return p, nil
}

// lookup finds the value at a path without [*] or [#]. ok is false when
// the path does not exist in value.
func lookup(value any, p path) (found any, ok bool) {
	for _, s := range p {
		switch s.kind {
		case stepKey:
			object, isObject := value.(map[string]any)
			if !isObject {
				return nil, false
			}
			if value, ok = object[s.key]; !ok {
				return nil, false
			}
		case stepIndex:
			array, isArray := value.([]any)
			if !isArray || s.index >= len(array) {
				return nil, false
			}
			value = array[s.index]
		default:
			return nil, false
		}
	}
	return value, true
}

// volatileSet is a set of paths whose values change from run to run. A
// value at such a path is compared by its JSON type only; an array whose
// length is in the set ([#]) may have any number of elements.
type volatileSet struct {
	root *volatileNode
}

type volatileNode struct {
	value  bool
	length bool
	// mark is what stays checked of a volatile value here.
	mark  *VolatileMark
	keys  map[string]*volatileNode
	index map[int]*volatileNode
	every *volatileNode
}

func newVolatileSet(paths []string) (*volatileSet, error) {
	return newVolatileSetOf(plainMarks(paths))
}

// newVolatileSetOf builds the set for marks, with their formats.
func newVolatileSetOf(marks []VolatileMark) (*volatileSet, error) {
	set := &volatileSet{root: &volatileNode{}}
	for i := range marks {
		p, err := parsePath(marks[i].Path)
		if err != nil {
			return nil, err
		}
		if node := set.add(p); node != nil {
			node.mark = &marks[i]
		}
	}
	return set, nil
}

// add puts a path into the set and returns the node of its value, or nil
// for a length mark.
func (set *volatileSet) add(p path) *volatileNode {
	node := set.root
	for _, s := range p {
		switch s.kind {
		case stepKey:
			if node.keys == nil {
				node.keys = map[string]*volatileNode{}
			}
			if node.keys[s.key] == nil {
				node.keys[s.key] = &volatileNode{}
			}
			node = node.keys[s.key]
		case stepIndex:
			if node.index == nil {
				node.index = map[int]*volatileNode{}
			}
			if node.index[s.index] == nil {
				node.index[s.index] = &volatileNode{}
			}
			node = node.index[s.index]
		case stepEvery:
			if node.every == nil {
				node.every = &volatileNode{}
			}
			node = node.every
		case stepLength:
			node.length = true
			return nil
		}
	}
	node.value = true
	return node
}

// volatileAt is where a walk through a body stands in a volatileSet: the
// nodes of the set that match the path walked so far.
type volatileAt []*volatileNode

func (set *volatileSet) at() volatileAt {
	if set == nil {
		return nil
	}
	return volatileAt{set.root}
}

// isValue reports whether the value here is volatile.
func (at volatileAt) isValue() bool {
	for _, node := range at {
		if node.value {
			return true
		}
	}
	return false
}

// marks lists what stays checked of the volatile value here.
func (at volatileAt) marks() []*VolatileMark {
	var marks []*VolatileMark
	for _, node := range at {
		if node.value && node.mark != nil {
			marks = append(marks, node.mark)
		}
	}
	return marks
}

// isLength reports whether the array here has a volatile length.
func (at volatileAt) isLength() bool {
	for _, node := range at {
		if node.length {
			return true
		}
	}
	return false
}

func (at volatileAt) key(key string) volatileAt {
	var next volatileAt
	for _, node := range at {
		if child := node.keys[key]; child != nil {
			next = append(next, child)
		}
	}
	return next
}

func (at volatileAt) element(index int) volatileAt {
	var next volatileAt
	for _, node := range at {
		if child := node.index[index]; child != nil {
			next = append(next, child)
		}
		if node.every != nil {
			next = append(next, node.every)
		}
	}
	return next
}

// jsonType names the JSON type of a decoded value.
func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case json.Number, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", value)
}
