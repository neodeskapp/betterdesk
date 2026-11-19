package main

import (
	"math"
	"strconv"
	"testing"
)

// The expected values come from the seed's own functions in Node.

// tenth is 0.1 as a variable, so tenth + 0.2 is computed in float64 and
// not folded exactly at compile time.
var tenth = 0.1

func TestGeneratorMatchesTheNodeSeed(t *testing.T) {
	cases := []struct {
		text  string
		seed  uint32
		first [3]float64
	}{
		{"", 2166136261, [3]float64{0.6112444521859288, 0.4935242917854339, 0.7740248835179955}},
		{"inv900100001", 1824630413, [3]float64{0.10700494959019125, 0.17192254471592605, 0.7622015876695514}},
		{"hist900100012", 1562619570, [3]float64{0.8614536598324776, 0.8395194541662931, 0.4784665412735194}},
		{"demo_operator0", 3361905569, [3]float64{0.24405266880057752, 0.2734801364131272, 0.908062991918996}},
		// Code units, not bytes: the first unit of each character
		{"é€😀", 2335863519, [3]float64{0.13250660640187562, 0.4537396137602627, 0.632589113432914}},
	}
	for _, c := range cases {
		if got := seedFor(c.text); got != c.seed {
			t.Errorf("seedFor(%q) = %d, want %d", c.text, got, c.seed)
		}
		r := rng(c.seed)
		for i, want := range c.first {
			if got := r(); got != want {
				t.Errorf("rng(%d) value %d = %v, want %v", c.seed, i+1, got, want)
			}
		}
	}
	if got := "demo" + strconv.FormatUint(uint64(seedFor("demo_operator0")), 16); got != "democ8629ba1" {
		t.Errorf("fingerprint %s", got)
	}
}

func TestTruthAndEquality(t *testing.T) {
	for _, v := range []any{nil, undefined, false, 0.0, math.NaN(), ""} {
		if truthy(v) {
			t.Errorf("%#v is truthy", v)
		}
	}
	for _, v := range []any{true, 1.0, -1.0, "0", map[string]any{}, []any{}} {
		if !truthy(v) {
			t.Errorf("%#v is falsy", v)
		}
	}
	if strictEqual(1.0, "1") || !strictEqual("a", "a") || strictEqual(nil, undefined) || !strictEqual(undefined, prop(nil, "x")) {
		t.Error("strictEqual does not follow ===")
	}
	if or(0.0, "x") != "x" || or("a", "x") != "a" || and(nil, "x") != nil || and(1.0, "x") != "x" {
		t.Error("|| or && does not return the deciding value")
	}
	if toNumber("42") != 42 || toNumber(" 7 ") != 7 || toNumber("") != 0 || !math.IsNaN(toNumber("12abc")) || toNumber("0x10") != 16 || toNumber(nil) != 0 || !math.IsNaN(toNumber(undefined)) {
		t.Error("toNumber does not follow Number()")
	}
}

func TestNumbersAndJSONAsJavaScriptWritesThem(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{
		{1e21, "1e+21"}, {1e-7, "1e-7"}, {123456789012, "123456789012"}, {tenth + 0.2, "0.30000000000000004"},
		{math.Copysign(0, -1), "0"}, {5, "5"},
	} {
		if got := numberString(c.in); got != c.want {
			t.Errorf("numberString(%v) = %s, want %s", c.in, got, c.want)
		}
	}
	for _, c := range []struct{ in, want float64 }{{-2.5, -2}, {2.5, 3}, {0.49999999999999994, 0}, {1.5, 2}} {
		if got := round(c.in); got != c.want {
			t.Errorf("round(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	got := mustStringify(object{{"a", "<&>"}, {"b", undefined}, {"c", []any{undefined, 1.5, 1e21, 1e-7, math.Copysign(0, -1)}}})
	if want := `{"a":"<&>","c":[null,1.5,1e+21,1e-7,0]}`; got != want {
		t.Errorf("stringify: %s, want %s", got, want)
	}
}
