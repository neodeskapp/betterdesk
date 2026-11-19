package crossplatform

import (
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// tally counts names. It is written as a JSON object with the names in the
// order JavaScript lists the keys of an object: whole numbers first, then
// the others as they came. Every name counts from 1, also the names every
// JavaScript object has a member for ("constructor", "toString",
// "__proto__").
type tally struct {
	counts *goapi.Object
}

func newTally() *tally { return &tally{counts: goapi.NewObject()} }

// add counts one more of name.
func (t *tally) add(name string) {
	count, _ := t.counts.Get(name).(float64)
	t.counts.Set(name, count+1)
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
