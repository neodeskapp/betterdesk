package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// substValue is the value of a substitution as merge-env.js held it. text
// is what the template gets, String(value ?? ""): "" for null, which null
// marks.
type substValue struct {
	text string
	null bool
}

// errSubstSyntax is JSON.parse's SyntaxError.
var errSubstSyntax = errors.New("Unexpected token in JSON")

// readSubstFile adds the members of a substitution file to subs as
// merge-env.js did: Object.assign(substitutions, JSON.parse(text)). An
// object adds its members, an array its elements under "0", "1" and so
// on, a string each of its UTF-16 code units the same way; a number, a
// boolean or null adds nothing. A member's value becomes text as String()
// makes it: a number as JavaScript writes it, an array as its elements
// joined with commas, an object as "[object Object]".
func readSubstFile(subs *object[substValue], raw []byte) error {
	text := []byte(serverinfo.DecodeUTF8(raw))
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	var top json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return errSubstSyntax
	}
	if strings.Trim(string(text[dec.InputOffset():]), " \t\n\r") != "" {
		return errSubstSyntax
	}
	top = bytes.TrimLeft(top, " \t\n\r")
	switch {
	case len(top) > 0 && top[0] == '{':
		return readSubstObject(subs, top)
	case len(top) > 0 && top[0] == '[':
		var items []json.RawMessage
		if err := json.Unmarshal(top, &items); err != nil {
			return errSubstSyntax
		}
		for i, item := range items {
			v, err := jsString(item)
			if err != nil {
				return err
			}
			subs.set(strconv.Itoa(i), v)
		}
	case len(top) > 0 && top[0] == '"':
		var s string
		if err := json.Unmarshal(top, &s); err != nil {
			return errSubstSyntax
		}
		// Half of a pair is a string of its own; string() makes it U+FFFD,
		// which is how Node writes it out
		for i, unit := range utf16.Encode([]rune(s)) {
			subs.set(strconv.Itoa(i), substValue{text: string(rune(unit))})
		}
	}
	return nil
}

func readSubstObject(subs *object[substValue], raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil {
		return errSubstSyntax
	}
	// JSON.parse keeps the last value of a name at the place of its first
	members := newObject[substValue]()
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return errSubstSyntax
		}
		key, ok := token.(string)
		if !ok {
			return errSubstSyntax
		}
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return errSubstSyntax
		}
		v, err := jsString(item)
		if err != nil {
			return err
		}
		// "__proto__" is an own member of the parsed object, but assigning
		// it to the substitutions sets their prototype: set leaves it out
		members.set(key, v)
	}
	for _, key := range members.entries() {
		subs.set(key, members.values[key])
	}
	return nil
}

// jsString is String(value) of a JSON value.
func jsString(raw json.RawMessage) (substValue, error) {
	raw = bytes.TrimLeft(raw, " \t\n\r")
	if len(raw) == 0 {
		return substValue{}, errSubstSyntax
	}
	switch raw[0] {
	case 'n':
		return substValue{null: true}, nil
	case 't', 'f':
		return substValue{text: string(raw)}, nil
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return substValue{}, errSubstSyntax
		}
		return substValue{text: s}, nil
	case '{':
		return substValue{text: "[object Object]"}, nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return substValue{}, errSubstSyntax
		}
		parts := make([]string, len(items))
		for i, item := range items {
			v, err := jsString(item)
			if err != nil {
				return substValue{}, err
			}
			// join writes null as ""
			parts[i] = v.text
		}
		return substValue{text: strings.Join(parts, ",")}, nil
	}
	// Too large a number is infinite, too small a one 0, as in JavaScript
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return substValue{}, errSubstSyntax
	}
	return substValue{text: jsbind.NumberString(f)}, nil
}

// substFileError is how a substitution file that cannot be read or
// parsed is reported (merge-env.js threw and Node printed the stack).
func substFileError(path string, err error) error {
	return fmt.Errorf("substitution file %s: %w", path, err)
}
