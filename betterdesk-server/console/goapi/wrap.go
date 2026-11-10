package goapi

import (
	"context"
	"net/http"
)

// The methods in this package return what the functions of the Node
// client returned. Most of them return one of these objects:
//
//	W   {success:true, data}. When the answer is an object with an "error"
//	    key, also on a 2xx status, it is {success:false, error} instead.
//	F1  {success:false, error: <message>}: the message of the failed call,
//	    such as "Request failed with status code 404". The error text the
//	    API server sent is not in it.
//	F2  W of the error body when the API server sent one, else F1.
//	F3  F2 plus "status": the HTTP status, or 502 when there was no answer.
//
// The doc comment of each method names its route and which of these it
// gives on failure.

// failKind says what a wrapped call returns when it fails.
type failKind int

const (
	f1 failKind = iota + 1
	f2
	f3
)

// do is Request with the concrete error type.
func (c *Client) do(ctx context.Context, method, path string, opts ...CallOption) (*Response, *Error) {
	resp, err := c.Request(ctx, method, path, opts...)
	if err != nil {
		return resp, AsError(err)
	}
	return resp, nil
}

// data runs a call and returns response.data.
func (c *Client) data(ctx context.Context, method, path string, opts ...CallOption) (any, *Error) {
	resp, e := c.do(ctx, method, path, opts...)
	if e != nil {
		return nil, e
	}
	return resp.Data(), nil
}

// wrap is the Node client's wrap(): see W above.
func wrap(data any) *Object {
	if o, ok := data.(*Object); ok && o != nil && o.Has("error") {
		return NewObject("success", false, "error", o.Get("error"))
	}
	return NewObject("success", true, "data", data)
}

// fail builds the failure object of the given kind for a failed call.
func fail(kind failKind, e *Error) *Object {
	if kind != f1 && e.Response != nil {
		if body := e.Response.Data(); Truthy(body) {
			o := wrap(body)
			if kind == f3 {
				o.Set("status", float64(e.Response.Status))
			}
			return o
		}
	}
	o := NewObject("success", false, "error", e.Message)
	if kind == f3 {
		status := http.StatusBadGateway
		if e.Response != nil && e.Response.Status != 0 {
			status = e.Response.Status
		}
		o.Set("status", float64(status))
	}
	return o
}

// wrapped runs a call and returns W, or the failure object of the kind.
func (c *Client) wrapped(ctx context.Context, kind failKind, method, path string, opts ...CallOption) *Object {
	data, e := c.data(ctx, method, path, opts...)
	if e != nil {
		return fail(kind, e)
	}
	return wrap(data)
}

// seg escapes one path segment the way the Node client did.
func seg(v string) string { return EncodeURIComponent(v) }

// safeID is the Node client's assertSafeApiId: it trims the value and
// accepts letters, digits and . _ : @ % + -, up to 256 of them.
func safeID(value, label string) (string, *Error) {
	v := jsTrim(value)
	if v == "" || v == "." || v == ".." || len(v) > 256 {
		return "", &Error{Message: "Invalid " + label}
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == ':' || c == '@' || c == '%' || c == '+' || c == '-':
		default:
			return "", &Error{Message: "Invalid " + label}
		}
	}
	return v, nil
}

// propOf reads a property the way JavaScript does when the value may be
// null: reading from null fails with a TypeError, whose message the Node
// client then returned as the error.
func propOf(data any, name string) (any, *Error) {
	if data == nil {
		return nil, &Error{Message: "Cannot read properties of null (reading '" + name + "')"}
	}
	return prop(data, name), nil
}

// listOf is the Node client's "Array.isArray(data) ? data : (data[name] ||
// [])" followed by a use of the result as an array.
func listOf(data any, name string) ([]any, *Error) {
	if arr, ok := data.([]any); ok {
		return arr, nil
	}
	inner, e := propOf(data, name)
	if e != nil {
		return nil, e
	}
	if !Truthy(inner) {
		return []any{}, nil
	}
	arr, ok := inner.([]any)
	if !ok {
		return nil, &Error{Message: name + ".map is not a function"}
	}
	return arr, nil
}

// spread is JavaScript's {...v} for a parsed JSON value.
func spread(v any) *Object {
	switch t := v.(type) {
	case *Object:
		return t.Clone()
	case []any:
		o := NewObject()
		for i, el := range t {
			o.Set(itoa(i), el)
		}
		return o
	case string:
		o := NewObject()
		for i, r := range []rune(t) {
			o.Set(itoa(i), string(r))
		}
		return o
	}
	return NewObject()
}
