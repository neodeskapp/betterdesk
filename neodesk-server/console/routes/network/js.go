package network

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The old routes took the members of a body and of a query as they came,
// whatever their type. The functions here are the JavaScript expressions
// they applied to them.

// text is String(v).
func text(v middleware.Value) string {
	switch v.Kind() {
	case middleware.KindUndefined:
		return "undefined"
	case middleware.KindNull:
		return "null"
	case middleware.KindBool:
		if b, _ := v.Bool(); b {
			return "true"
		}
		return "false"
	case middleware.KindNumber:
		f, _ := v.Float()
		return netmonitor.Number(f).String()
	case middleware.KindString:
		s, _ := v.Str()
		return s
	case middleware.KindArray:
		items, _ := v.Array()
		parts := make([]string, len(items))
		for i, item := range items {
			if k := item.Kind(); k != middleware.KindNull && k != middleware.KindUndefined {
				parts[i] = text(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// parseInt is parseInt(v, 10).
func parseInt(v middleware.Value) netmonitor.Number {
	return netmonitor.ParseInt(text(v))
}

// stringify is JSON.stringify(v) for a value that came out of JSON.parse:
// no white space, numbers as JavaScript prints them, keys that are array
// indexes first.
func stringify(v middleware.Value) json.RawMessage {
	var parsed store.JSONParsed
	if err := parsed.Scan(string(v.Raw())); err != nil {
		return json.RawMessage("null")
	}
	out, err := parsed.MarshalJSON()
	if err != nil {
		return json.RawMessage("null")
	}
	return out
}

// member is one key of an answer with its value.
type member struct {
	key   string
	value any
}

// object is a JSON object whose keys keep the order they were added in,
// for the answers the old routes built by spreading a result into an
// object.
type object []member

// MarshalJSON implements json.Marshaler.
func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := httpx.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		value, err := httpx.Marshal(m.value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// result spreads a check result into an answer: success, rtt_ms, for an
// HTTP check status_code, and error.
func (o object) result(res netmonitor.Result) object {
	o = append(o, member{"success", res.Success}, member{"rtt_ms", res.RTT})
	if res.HTTP {
		o = append(o, member{"status_code", res.StatusCode})
	}
	return append(o, member{"error", res.Error})
}

// queryValue is a member of req.query: nothing, a string, a list of
// strings, or an object.
type queryValue struct {
	kind  queryKind
	text  string
	items []string
}

type queryKind uint8

const (
	queryUndefined queryKind = iota
	queryString
	queryList
	queryObject
)

// query reads one member of the query string the way the old console's
// parser built it: "a=1" is a string, "a=1&a=2", "a[]=1" and "a[0]=1" are
// lists, "a[x]=1" is an object. Deeper nesting is read as an object of
// strings.
func query(rawQuery, name string) queryValue {
	var v queryValue
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key, value := queryDecode(rawKey), queryDecode(rawValue)
		switch {
		case key == name:
			v.items = append(v.items, value)
		case strings.HasPrefix(key, name+"[") && strings.HasSuffix(key, "]"):
			inner := key[len(name)+1 : len(key)-1]
			if inner != "" && strings.Trim(inner, "0123456789") != "" {
				v.kind = queryObject
			} else if v.kind != queryObject {
				v.kind = queryList
			}
			v.items = append(v.items, value)
		}
	}
	switch {
	case len(v.items) == 0:
		v.kind = queryUndefined
	case v.kind == queryUndefined && len(v.items) == 1:
		v.kind, v.text = queryString, v.items[0]
	case v.kind == queryUndefined:
		v.kind = queryList
	}
	return v
}

// queryDecode decodes a key or value of a query string. Text with a
// broken escape stays as it is.
func queryDecode(s string) string {
	decoded, err := url.QueryUnescape(s)
	if err != nil {
		return strings.ReplaceAll(s, "+", " ")
	}
	return decoded
}
