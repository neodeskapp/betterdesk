package alerts

import (
	"errors"
	"math"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// errNotPrimitive is the TypeError JavaScript throws when it needs text or
// a number of an object that has a member named toString: the member
// stands in the way of the method every object has.
var errNotPrimitive = errors.New("Cannot convert object to primitive value")

// jsString is String(v), with the error JavaScript throws for an object
// it cannot make text of, also inside a list.
func jsString(v jsbind.Value) (string, error) {
	switch {
	case v.IsObject():
		if !v.Member("toString").IsUndefined() {
			return "", errNotPrimitive
		}
		return "[object Object]", nil
	case v.IsArray():
		parts := make([]string, len(v.Items()))
		for i, item := range v.Items() {
			if item.IsUndefined() || item.IsNull() {
				continue
			}
			text, err := jsString(item)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	}
	return v.ToString(), nil
}

// jsNumber is Number(v), with the error of jsString for a list or an
// object, which become numbers by way of their text.
func jsNumber(v jsbind.Value) (float64, error) {
	if v.IsArray() || v.IsObject() {
		text, err := jsString(v)
		if err != nil {
			return 0, err
		}
		return jsbind.ParseNumber(text), nil
	}
	return v.ToNumber(), nil
}

// texts turns values into text one after the other and remembers the
// first that fails.
type texts struct{ err error }

func (t *texts) of(v jsbind.Value) string {
	if t.err != nil {
		return ""
	}
	text, err := jsString(v)
	if err != nil {
		t.err = err
	}
	return text
}

// strictEqual is a === b for two values of which one is a row's text:
// equal types and equal content. A list or an object equals nothing.
func strictEqual(a, b jsbind.Value) bool {
	switch {
	case a.IsString():
		return b.IsString() && a.Str() == b.Str()
	case a.IsNumber():
		return b.IsNumber() && a.ToNumber() == b.ToNumber()
	case a.IsNull():
		return b.IsNull()
	case a.IsUndefined():
		return b.IsUndefined()
	case a.IsArray() || a.IsObject():
		return false
	}
	// a flag
	return isBool(b) && a.Truthy() == b.Truthy()
}

func isBool(v jsbind.Value) bool {
	return !v.IsString() && !v.IsNumber() && !v.IsNull() && !v.IsUndefined() && !v.IsArray() && !v.IsObject()
}

// jsMax is Math.max(a, b): NaN when either is NaN.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}

// jsRound is Math.round(x): the nearest whole number, a half rounding
// up (2.5 is 3, -2.5 is -2).
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	floor := math.Floor(x)
	if x-floor >= 0.5 {
		return floor + 1
	}
	return floor
}

// anyString is String(v) for a value of the device list: nil, a flag, a
// number, text, a list or an object.
func anyString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "null", nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case float64:
		return jsbind.NumberString(t), nil
	case string:
		return t, nil
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item == nil {
				continue
			}
			text, err := anyString(item)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	case *goapi.Object:
		if t == nil {
			return "null", nil
		}
		if t.Has("toString") {
			return "", errNotPrimitive
		}
	}
	return "[object Object]", nil
}

// anyValue is a device id of the device list as a JavaScript value. ok is
// false for a list or an object, which the engine cannot bind to a
// statement the way the Node database drivers did.
func anyValue(v any) (jsbind.Value, bool) {
	switch t := v.(type) {
	case nil:
		return jsbind.Null, true
	case bool:
		return jsbind.Bool(t), true
	case float64:
		return jsbind.Number(t), true
	case string:
		return jsbind.String(t), true
	}
	return jsbind.Undefined, false
}
