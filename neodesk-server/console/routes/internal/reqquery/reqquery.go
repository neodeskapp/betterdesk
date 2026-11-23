// Package reqquery is req.query of the old console for route groups that
// need a whole query or the shape of a value: the object Express built
// from a query string with the qs package, where a repeated or bracketed
// parameter (a=1&a=2, a[]=1, a[x]=1) is a list or an object.
//
// Package goapipath answers the simpler question, one parameter as text
// (goapipath.Query). A group needs this package when it hands the whole
// query on (new URLSearchParams(req.query)) or passes a value on with its
// shape (the params of a call into the API server).
package reqquery

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// Query is req.query: one value per parameter, in the order JavaScript
// lists the keys of the object.
type Query struct {
	q *query
}

// Of reads the query of a request.
func Of(r *http.Request) *Query {
	return &Query{q: requestQuery(r)}
}

// Value is one value of a query: a text, a list or an object, or the
// value of a parameter the query does not have (undefined).
type Value struct {
	v       any
	defined bool
}

// Get is req.query[name].
func (q *Query) Get(name string) Value {
	v, ok := q.q.values.get(name)
	return Value{v: v, defined: ok}
}

// Has reports whether the query has the parameter.
func (q *Query) Has(name string) bool {
	_, ok := q.q.values.get(name)
	return ok
}

// Set gives a parameter a text value, as URLSearchParams.set does on the
// query written out: a parameter the query has keeps its place, a new one
// goes to the end.
func (q *Query) Set(name, value string) {
	q.q.set(name, value)
}

// Delete removes a parameter.
func (q *Query) Delete(name string) {
	values := newObject()
	for _, key := range q.q.values.keys() {
		if key == name {
			continue
		}
		v, _ := q.q.values.get(key)
		values.set(key, v)
	}
	q.q.values = values
}

// Search is new URLSearchParams(req.query).toString(): one name=value per
// parameter, form encoded, a list as its elements joined by commas and an
// object as "[object Object]". ok is false where JavaScript throws
// because a value cannot be turned into text (an object with a member
// named toString).
func (q *Query) Search() (text string, ok bool) {
	return q.q.search()
}

// Defined is value !== undefined.
func (v Value) Defined() bool { return v.defined }

// Truthy is !!value: false for a missing parameter and for the empty
// text, true for every other text, every list and every object.
func (v Value) Truthy() bool { return truthy(v.v) }

// Text is String(value), "undefined" for a missing parameter. ok is false
// where JavaScript throws.
func (v Value) Text() (text string, ok bool) {
	if !v.defined {
		return "undefined", true
	}
	return toText(v.v)
}

// IsText reports a value that is a text and returns it.
func (v Value) IsText() (string, bool) {
	text, ok := v.v.(string)
	return text, ok && v.defined
}

// JSON is the value as package goapi holds JSON: a string, a []any or a
// *goapi.Object, nil for a missing parameter. It is what a call into the
// API server takes as a parameter (goapi.Query).
func (v Value) JSON() any {
	if !v.defined {
		return nil
	}
	return toJSON(v.v)
}

func toJSON(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = toJSON(item)
		}
		return out
	case *object:
		out := goapi.NewObject()
		for _, key := range t.keys() {
			item, _ := t.get(key)
			out.Set(key, toJSON(item))
		}
		return out
	}
	return v
}
