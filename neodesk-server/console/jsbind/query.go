package jsbind

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// queryParts is the number of pairs the old console's query parser read
// (parameterLimit), and queryList the number of values a list could hold
// before the parser turned it into an object (arrayLimit).
const (
	queryParts = 1000
	queryList  = 20
)

// Query is req.query[name]: the value the old console's query parser (qs,
// as Express set it up) made of a parameter of the raw query string.
//
//	no such parameter            undefined
//	name=a                       the text "a"
//	name=a&name=b                the list ["a","b"]
//	name[]=a&name[]=b            the list ["a","b"], also with one value
//	name[1]=b&name[0]=a          the list ["a","b"], by index
//	name=a&name[]=b&name[5]=c    the list ["a","b","c"]: the forms mix
//
// Every other way to write the parameter (name[key]=, nested brackets, an
// index given twice, more than 20 values) made an object or a list that
// holds lists. Query answers all of those with one value, see IsComplex:
// no statement of the console could do anything with them but fail or
// find nothing.
func Query(rawQuery, name string) Value {
	parts := strings.Split(bracketEscapes.Replace(rawQuery), "&")
	if len(parts) > queryParts {
		parts = parts[:queryParts]
	}
	// The values of each spelling of the key, in the order the spellings
	// first appear: the parser collected them per spelling and merged the
	// spellings afterwards.
	var keys []string
	values := map[string][]string{}
	for _, part := range parts {
		pos := strings.Index(part, "]=")
		if pos >= 0 {
			pos++
		} else {
			pos = strings.IndexByte(part, '=')
		}
		key, value := part, ""
		if pos >= 0 {
			key, value = part[:pos], part[pos+1:]
		}
		key = queryDecode(key)
		if key != name && !strings.HasPrefix(key, name+"[") {
			continue
		}
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = append(values[key], queryDecode(value))
	}
	if len(keys) == 0 {
		return Undefined
	}

	complex := Value{kind: kindObject, complex: true}
	var merged queryValue
	for i, key := range keys {
		given := values[key]
		if len(given) > queryList {
			return complex
		}
		var leaf queryValue
		form, index := keyForm(key[len(name):])
		switch {
		case form == formPlain && len(given) == 1:
			leaf = queryValue{text: given[0]}
		case form == formPlain, form == formList:
			leaf = queryValue{list: true, items: map[int]string{}, length: len(given)}
			for at, value := range given {
				leaf.items[at] = value
			}
		case form == formIndex && len(given) == 1:
			leaf = queryValue{list: true, items: map[int]string{index: given[0]}, length: index + 1}
		default:
			return complex
		}
		if i == 0 {
			merged = leaf
			continue
		}
		var ok bool
		if merged, ok = merged.merge(leaf); !ok {
			return complex
		}
	}
	if !merged.list {
		return String(merged.text)
	}
	items := make([]Value, 0, len(merged.items))
	for at := 0; at < merged.length; at++ {
		if item, ok := merged.items[at]; ok {
			items = append(items, String(item))
		}
	}
	return Value{kind: kindArray, items: items}
}

// queryValue is a parameter while its spellings are merged: a text, or a
// list that may have gaps (name[3]=x is a list with one value at 3).
type queryValue struct {
	text   string
	list   bool
	items  map[int]string
	length int
}

// merge adds the value of a later spelling to the value so far. ok is
// false when the result had more than 20 values, which made it an object.
func (v queryValue) merge(source queryValue) (result queryValue, ok bool) {
	switch {
	case !source.list && source.text == "":
		// An empty text adds nothing
		return v, true
	case !source.list && !v.list:
		return queryValue{list: true, items: map[int]string{0: v.text, 1: source.text}, length: 2}, true
	case !source.list:
		v.items[v.length] = source.text
		v.length++
		return v, true
	case !v.list:
		// The text goes in front of the list
		shifted := queryValue{list: true, items: map[int]string{0: v.text}, length: source.length + 1}
		for at, item := range source.items {
			shifted.items[at+1] = item
		}
		return shifted, shifted.length <= queryList
	}
	// Two lists: a value whose place is free takes it, the others go to
	// the end
	for at := 0; at < source.length; at++ {
		item, present := source.items[at]
		if !present {
			continue
		}
		place := at
		if _, taken := v.items[at]; taken {
			place = v.length
		}
		v.items[place] = item
		if place >= v.length {
			v.length = place + 1
		}
	}
	return v, len(v.items) <= queryList
}

const (
	formPlain = iota
	formList
	formIndex
	formOther
)

// queryDepth is the number of bracket groups of a key the parser took
// apart; what follows them was one name.
const queryDepth = 5

// keyForm reads what follows the name in a key: nothing (plain), groups of
// empty brackets (a list), such groups with an index in the last one
// ("[3]", "[][3]"), or something else. Text between and after the groups
// does not count.
func keyForm(rest string) (form, index int) {
	if rest == "" {
		return formPlain, 0
	}
	var groups []string
	open := 0
	for open >= 0 && len(groups) < queryDepth {
		level, end := 1, -1
		for i := open + 1; i < len(rest) && end < 0; i++ {
			switch rest[i] {
			case '[':
				level++
			case ']':
				if level--; level == 0 {
					end = i
				}
			}
		}
		if end < 0 {
			return formOther, 0
		}
		groups = append(groups, rest[open+1:end])
		if open = strings.IndexByte(rest[end+1:], '['); open >= 0 {
			open += end + 1
		}
	}
	if open >= 0 {
		return formOther, 0
	}
	last := groups[len(groups)-1]
	for _, group := range groups[:len(groups)-1] {
		if group != "" {
			return formOther, 0
		}
	}
	if last == "" {
		return formList, 0
	}
	// Text that is no number reads as 0 and is not written "0"
	n, _ := strconv.Atoi(last)
	if n < 0 || n >= queryList || strconv.Itoa(n) != last {
		return formOther, 0
	}
	return formIndex, n
}

// bracketEscapes undoes the percent-encoding of brackets before the query
// is split, as the parser did.
var bracketEscapes = strings.NewReplacer("%5B", "[", "%5b", "[", "%5D", "]", "%5d", "]")

// queryDecode decodes a key or a value: a plus is a space, and percent
// escapes are undone when all of them are well formed and give UTF-8.
// Otherwise the text stays as it is, with the pluses replaced.
func queryDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if !strings.Contains(s, "%") {
		return s
	}
	decoded, err := url.PathUnescape(s)
	if err != nil || !utf8.ValidString(decoded) {
		return s
	}
	return decoded
}

// IsComplex reports whether v is a query parameter in a form Query does
// not take apart. Such a value is an object for IsObject and Truthy. As a
// statement parameter it is an error on SQLite and the text "{}" on
// PostgreSQL.
func (v Value) IsComplex() bool { return v.complex }

// ErrNotOneText is QueryText's answer for a parameter the old console's
// query parser made a list or an object of.
var ErrNotOneText = errors.New("not one text")

// QueryText reads a query parameter that takes one text, such as the
// filter of a list: its text, and whether the query names it at all. The
// error is ErrNotOneText when the old console's query parser (qs as
// Express 4.22 set it up, which goapipath follows step by step) made a
// list or an object of the parameter: given twice, with brackets after
// the name (name[]=, name[0]=, name[key]=), or beside such a spelling.
// rawQuery is the text after the "?" as the client sent it; a "#" ends
// it, and only the first 1000 pairs count. A byte above 0x7F is the
// Latin-1 character with that number, as the old console's HTTP parser
// read it.
//
// The parser made a text of the parameter when the first key that names
// it names it plainly ("name" or "[name]") and comes once, and every later
// key that names it does so plainly, once and with an empty value. name
// must be no whole number.
func QueryText(rawQuery, name string) (text string, given bool, err error) {
	raw, _, _ := strings.Cut(rawQuery, "#")
	parts := strings.Split(bracketEscapes.Replace(httpx.Latin1(raw)), "&")
	if len(parts) > queryParts {
		parts = parts[:queryParts]
	}
	// The values of each key that names the parameter, by the key as it
	// was decoded, in the order the keys first appear
	type spelling struct {
		plain  bool
		values []string
	}
	var spellings []*spelling
	seen := map[string]*spelling{}
	for _, part := range parts {
		key, value := splitPair(part)
		under, plain := namesParameter(key, name)
		if !under {
			continue
		}
		s := seen[key]
		if s == nil {
			s = &spelling{plain: plain}
			seen[key] = s
			spellings = append(spellings, s)
		}
		s.values = append(s.values, value)
	}
	if len(spellings) == 0 {
		return "", false, nil
	}
	first := spellings[0]
	if !first.plain || len(first.values) != 1 {
		return "", true, ErrNotOneText
	}
	for _, s := range spellings[1:] {
		if !s.plain || len(s.values) != 1 || s.values[0] != "" {
			return "", true, ErrNotOneText
		}
	}
	return first.values[0], true, nil
}

// splitPair splits a pair of the query into its decoded key and value as
// the parser did: after the "]" of the first "]=", else at the first "=".
func splitPair(part string) (key, value string) {
	pos := strings.Index(part, "]=")
	if pos >= 0 {
		pos++
	} else {
		pos = strings.IndexByte(part, '=')
	}
	if pos < 0 {
		return queryDecode(part), ""
	}
	return queryDecode(part[:pos]), queryDecode(part[pos+1:])
}

// namesParameter reports whether a decoded key is a spelling of the
// parameter, and whether it names the parameter plainly (as a text) and
// not as a list or an object. The parser took the text before the first
// "[" for the name; a key that starts with a bracket group took the text
// inside the group, and another group after it made an object.
func namesParameter(key, name string) (under, plain bool) {
	first := strings.IndexByte(key, '[')
	switch {
	case key == "":
		return false, false
	case first < 0:
		return key == name, true
	case first > 0:
		return key[:first] == name, false
	}
	level, closed := 1, -1
	for i := 1; i < len(key) && closed < 0; i++ {
		switch key[i] {
		case '[':
			level++
		case ']':
			if level--; level == 0 {
				closed = i
			}
		}
	}
	if closed < 0 || key[1:closed] != name {
		return false, false
	}
	return true, strings.IndexByte(key[closed+1:], '[') < 0
}
