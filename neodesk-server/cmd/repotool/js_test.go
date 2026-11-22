package main

import (
	"math"
	"reflect"
	"testing"
)

func TestJSNumber(t *testing.T) {
	for f, want := range map[float64]string{
		0:           "0",
		5:           "5",
		-1.5:        "-1.5",
		1000000:     "1000000",
		1e20:        "100000000000000000000",
		1e21:        "1e+21",
		1.5e300:     "1.5e+300",
		0.1:         "0.1",
		0.000001:    "0.000001",
		1e-7:        "1e-7",
		-1.5e-7:     "-1.5e-7",
		123.456:     "123.456",
		math.Inf(1): "Infinity",
	} {
		if got := jsNumber(f); got != want {
			t.Errorf("jsNumber(%v) = %q, want %q", f, got, want)
		}
	}
}

func TestJSTrimAndLen(t *testing.T) {
	bom, nbsp, nel := string(rune(0xFEFF)), string(rune(0xA0)), string(rune(0x85))
	if got := jsTrim(bom + nbsp + " a b\t\n" + bom); got != "a b" {
		t.Errorf("jsTrim = %q", got)
	}
	// NEL is no whitespace in JavaScript
	if got := jsTrim(nel + "a"); got != nel+"a" {
		t.Errorf("jsTrim kept %q", got)
	}
	if n := jsLen("a" + string(rune(0x1F600)) + "é"); n != 4 {
		t.Errorf("jsLen = %d, want 4", n)
	}
}

func TestParseJSONOrder(t *testing.T) {
	v, err := parseJSON([]byte(`{"b":1,"10":2,"2":3,"a":{"z":null,"y":[1,"x",null,true]},"b":4,"01":5,"4294967295":6,"4294967294":7}`))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(*jsObject)
	// Array indexes ascending first, then the rest as they came; a key
	// given twice keeps its first place and its last value
	if want := []string{"2", "10", "4294967294", "b", "a", "01", "4294967295"}; !reflect.DeepEqual(obj.keys, want) {
		t.Errorf("keys %v, want %v", obj.keys, want)
	}
	if b, _ := obj.get("b"); b != float64(4) {
		t.Errorf("b = %v", b)
	}
	a, _ := obj.get("a")
	y, _ := a.(*jsObject).get("y")
	if got := jsString(y); got != "1,x,,true" {
		t.Errorf("String(array) = %q", got)
	}
	for _, bad := range []string{`{"a":1} 2`, `{"a":`, ``, `{a:1}`} {
		if _, err := parseJSON([]byte(bad)); err == nil {
			t.Errorf("parseJSON(%q) did not fail", bad)
		}
	}
}

func TestJSTruthy(t *testing.T) {
	for v, want := range map[any]bool{nil: false, "": false, "x": true, float64(0): false, float64(2): true, false: false, true: true} {
		if got := jsTruthy(v); got != want {
			t.Errorf("jsTruthy(%v) = %v", v, got)
		}
	}
	if !jsTruthy(newJSObject()) || !jsTruthy([]any{}) {
		t.Error("objects and arrays are truthy")
	}
}
