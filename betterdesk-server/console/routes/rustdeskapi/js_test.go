package rustdeskapi

import (
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

func TestSanitize(t *testing.T) {
	for _, c := range []struct {
		in   any
		max  int
		want string
	}{
		{"plain", 32, "plain"},
		{"a\x00b\x07c\x1fd\te\nf\rg\x0bh\x0ci", 64, "abcd\te\nf\rghi"},
		{"abcdef", 3, "abc"},
		{"ab😀cd", 3, "ab�"},
		{"ab😀cd", 4, "ab😀"},
		{5.0, 32, ""},
		{nil, 32, ""},
		{[]any{"a"}, 32, ""},
	} {
		if got := sanitize(c.in, c.max); got != c.want {
			t.Errorf("sanitize(%#v, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestStringOfAValue(t *testing.T) {
	obj := goapi.NewObject("a", 1.0)
	withToString := goapi.NewObject("toString", 1.0)
	for _, c := range []struct {
		in     any
		want   string
		throws bool
	}{
		{undefined{}, "undefined", false},
		{nil, "null", false},
		{true, "true", false},
		{1.5, "1.5", false},
		{1e21, "1e+21", false},
		{[]any{1.0, []any{2.0, 3.0}, nil}, "1,2,3,", false},
		{obj, "[object Object]", false},
		{withToString, "", true},
		{[]any{withToString}, "", true},
	} {
		got, err := str(c.in)
		if (err != nil) != c.throws || got != c.want {
			t.Errorf("str(%#v) = %q, %v; want %q, throws %t", c.in, got, err, c.want, c.throws)
		}
	}
}

func TestStringifyIsJSONStringify(t *testing.T) {
	v, err := goapi.Parse([]byte(`{"2":"two","1":"one","b":[-0,1e-7,1e21,0.1,123456789012345678901],"s":" é</script>\u0001\"\\"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"1\":\"one\",\"2\":\"two\",\"b\":[0,1e-7,1e+21,0.1,123456789012345680000],\"s\":\" é</script>\\u0001\\\"\\\\\"}"
	if got := stringify(v); got != want {
		t.Errorf("stringify = %s\n            want %s", got, want)
	}
}

func TestParseIntAndFloat(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
	}{
		{"42", 42}, {"  -7x", -7}, {"+3", 3}, {"x", math.NaN()}, {"", math.NaN()}, {"0x10", 0},
	} {
		if got := parseIntText(c.in); !(got == c.want || math.IsNaN(got) && math.IsNaN(c.want)) {
			t.Errorf("parseInt(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   string
		want float64
	}{
		{"5.2", 5.2}, {"5.", 5}, {" .5x", 0.5}, {"1e3", 1000}, {"-Infinity", math.Inf(-1)}, {"x", math.NaN()},
	} {
		if got := parseFloatJS(c.in); !(got == c.want || math.IsNaN(got) && math.IsNaN(c.want)) {
			t.Errorf("parseFloat(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseCPU(t *testing.T) {
	for _, c := range []struct {
		raw        string
		name       string
		cores, ghz float64
	}{
		{"Intel Core i7-12700K, 5.2GHz, 24/16 cores", "Intel Core i7-12700K", 24, 5.2},
		{"AMD Ryzen 9, 4GHZ, 0/12 CORES", "AMD Ryzen 9", 12, 4},
		{", 5.GHz, 8 core", ", 5.GHz, 8 core", 8, 5},
		{"x, 12GHz, 4/ cores", "x", 4, 12},
		{"Apple M2", "Apple M2", 0, 0},
		{"", "", 0, 0},
	} {
		name, cores, ghz := parseCPU(c.raw)
		if name != c.name || cores != c.cores || ghz != c.ghz {
			t.Errorf("parseCPU(%q) = %q, %v, %v; want %q, %v, %v", c.raw, name, cores, ghz, c.name, c.cores, c.ghz)
		}
	}
}

func TestParseDateLikeNewDate(t *testing.T) {
	local := time.Date(2026, 11, 10, 20, 13, 11, 0, time.Local)
	for _, c := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2026-11-10 20:13:11", local, true},
		{"2026-11-10T20:13:11", local, true},
		{"2026-11-10T20:13:11Z", time.Date(2026, 11, 10, 20, 13, 11, 0, time.UTC), true},
		{"2026-11-10T20:13:11.5+01:00", time.Date(2026, 11, 10, 19, 13, 11, 500e6, time.UTC), true},
		{"2026-11-10", time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), true},
		{"garbage", time.Time{}, false},
		{"2026-02-31 00:00:00", time.Time{}, false},
	} {
		got, ok := parseDate(c.in)
		if ok != c.ok || ok && !got.Equal(c.want) {
			t.Errorf("parseDate(%q) = %v, %t; want %v, %t", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The answers of Buffer.from(text, 'base64') in Node 22.
func TestDecodeBase64LikeNode(t *testing.T) {
	for in, want := range map[string]string{
		"YQ==": "61", "YQ": "61", "YQ==YQ==": "61", "Y Q = =": "61", "Y*Q": "61",
		"-_-_": "fbffbf", "+/+/": "fbffbf", "YWJj": "616263", "YWJjZA": "61626364",
		"YWJjZA=": "61626364", "Y": "", "é": "", "": "", "YW=Jj": "61", "YWJ\nj": "616263",
	} {
		if got := hex.EncodeToString(decodeBase64Loose(in)); got != want {
			t.Errorf("decode %q = %s, want %s", in, got, want)
		}
	}
}

func TestPendingSecondSteps(t *testing.T) {
	s := &tfaSessions{}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.add("old", tfaSession{userID: 1, createdAt: start})
	if _, ok := s.get("old", start); !ok {
		t.Fatal("a pending step is not kept")
	}
	if _, ok := s.get(5.0, start); ok {
		t.Error("a secret that is no text matched")
	}
	// A step is good for five minutes, also when no other sign-in comes
	// in between (BUGS RD5)
	if _, ok := s.get("old", start.Add(5*time.Minute)); !ok {
		t.Error("an entry of exactly five minutes was refused")
	}
	if _, ok := s.get("old", start.Add(5*time.Minute+time.Millisecond)); ok {
		t.Error("an entry older than five minutes was found")
	}
	// A new step drops the ones older than five minutes
	s.add("new", tfaSession{userID: 2, createdAt: start.Add(5 * time.Minute)})
	s.add("newer", tfaSession{userID: 3, createdAt: start.Add(10*time.Minute + time.Millisecond)})
	if _, kept := s.pending["new"]; kept || len(s.pending) != 1 {
		t.Errorf("the steps left after a new one: %v", s.pending)
	}
	s.remove("newer")
	if _, ok := s.get("newer", start.Add(10*time.Minute)); ok {
		t.Error("a used secret works again")
	}
}

func TestDeviceIDs(t *testing.T) {
	for id, want := range map[string]bool{
		"900100001": true, "a_b-C": true, "": false, "bad id": false, "a/b": false,
		"12345678901234567890123456789012": true, "123456789012345678901234567890123": false, "é": false,
	} {
		if got := isValidDeviceID(id); got != want {
			t.Errorf("isValidDeviceID(%q) = %t", id, got)
		}
	}
}

func TestSysinfoLogThrottle(t *testing.T) {
	l := &logThrottle{}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !l.allow("a", at) || l.allow("a", at.Add(5*time.Minute)) || !l.allow("a", at.Add(5*time.Minute+time.Millisecond)) {
		t.Error("a device is logged other than once in five minutes")
	}
}
