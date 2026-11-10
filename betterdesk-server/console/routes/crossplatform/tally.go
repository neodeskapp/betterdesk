package crossplatform

import (
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// tally counts names. It is written as a JSON object with the names in the
// order JavaScript lists the keys of an object: whole numbers first, then
// the others as they came.
//
// A name that every JavaScript object already has a member for does not
// count from 1: the count starts as that member's text with a "1" added,
// and each further device adds another "1" (see BUGS.md). "__proto__" never
// becomes a name.
type tally struct {
	counts *goapi.Object
}

func newTally() *tally { return &tally{counts: goapi.NewObject()} }

// inherited holds the members of every JavaScript object as text.
var inherited = map[string]string{
	"constructor":          "function Object() { [native code] }",
	"__defineGetter__":     "function __defineGetter__() { [native code] }",
	"__defineSetter__":     "function __defineSetter__() { [native code] }",
	"hasOwnProperty":       "function hasOwnProperty() { [native code] }",
	"__lookupGetter__":     "function __lookupGetter__() { [native code] }",
	"__lookupSetter__":     "function __lookupSetter__() { [native code] }",
	"isPrototypeOf":        "function isPrototypeOf() { [native code] }",
	"propertyIsEnumerable": "function propertyIsEnumerable() { [native code] }",
	"toString":             "function toString() { [native code] }",
	"valueOf":              "function valueOf() { [native code] }",
	"toLocaleString":       "function toLocaleString() { [native code] }",
}

// add counts one more of name.
func (t *tally) add(name string) {
	if name == "__proto__" {
		return
	}
	switch current := t.counts.Get(name).(type) {
	case float64:
		t.counts.Set(name, current+1)
	case string:
		t.counts.Set(name, current+"1")
	default:
		if text, ok := inherited[name]; ok {
			t.counts.Set(name, text+"1")
			return
		}
		t.counts.Set(name, float64(1))
	}
}

// addEach counts what a device names in one field: each element of a
// list, or the one value. An empty field names nothing.
func (t *tally) addEach(v any) {
	if !goapi.Truthy(v) {
		return
	}
	list, isList := v.([]any)
	if !isList {
		list = []any{v}
	}
	for _, name := range list {
		t.add(jsreq.String(name))
	}
}

// MarshalJSON writes the counts as an object.
func (t *tally) MarshalJSON() ([]byte, error) {
	return t.counts.MarshalJSON()
}
