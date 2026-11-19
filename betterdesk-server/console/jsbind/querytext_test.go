package jsbind_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// QueryText gives a filter's text only where the old console's query
// parser made a text of it, and says "no single text" for every list and
// object it made (BUGS AU12): also beyond the 20 values and indexes
// Query takes apart, and for spellings Query does not know.
func TestQueryText(t *testing.T) {
	type want struct {
		text  string
		given bool
		one   bool
	}
	text := func(s string) want { return want{s, true, true} }
	none := want{"", false, true}
	notOne := want{"", true, false}
	many := strings.Repeat("x=1&", 999)
	for query, w := range map[string]want{
		"":                                    none,
		"other=1":                             none,
		"device_id=D1":                        text("D1"),
		"device_id=":                          text(""),
		"device_id":                           text(""),
		"device_id=a%2Bb+c":                   text("a+b c"),
		"%64evice_id=D1":                      text("D1"),
		"device_id=%zz":                       text("%zz"),
		"device_id=%C3%A9":                    text("é"),
		"device_id=%C3":                       text("%C3"),
		"device_id=\xc3\xa9":                  text("Ã©"),
		"device_id=a#device_id=b":             text("a"),
		"device_id=a&[device_id]=":            text("a"),
		"[device_id]=D1":                      text("D1"),
		"[device_id]x=D1":                     text("D1"),
		"device_id]=x":                        none,
		"device_id=a]=b":                      none,
		"device_id=a&device_id=b":             notOne,
		"device_id=a&device_id=":              notOne,
		"device_id=&device_id=":               notOne,
		"device_id=&[device_id]=b":            notOne,
		"device_id=a&[device_id]=b":           notOne,
		"device_id[]=a":                       notOne,
		"device_id[]":                         notOne,
		"device_id[0]=a":                      notOne,
		"device_id[25]=D1":                    notOne,
		"device_id[999]=D1":                   notOne,
		"device_id[id]=x":                     notOne,
		"device_id[=x":                        notOne,
		"device_id%5B%5D=x":                   notOne,
		"device_id%5b0%5d=x":                  notOne,
		"[device_id][a]=x":                    notOne,
		"device_id[id]=x&severity[]=D1":       notOne,
		many + "x=1&device_id=a":              none,
		"device_id=a&" + many + "device_id=b": text("a"),
	} {
		got, given, err := jsbind.QueryText(query, "device_id")
		if err != nil && !errors.Is(err, jsbind.ErrNotOneText) {
			t.Fatalf("QueryText(%q): %v", query, err)
		}
		if got != w.text || given != w.given || (err == nil) != w.one {
			t.Errorf("QueryText(%q) = %q, %v, %v; want %q, %v, one text %v", query, got, given, err, w.text, w.given, w.one)
		}
	}
}

// testdata/query.json holds what qs made of the parameter "name" in about
// 6,000 query strings: QueryText takes the texts and only them.
func TestQueryTextAsTheOldParserReadIt(t *testing.T) {
	raw, err := os.ReadFile("testdata/query.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Query string          `json:"query"`
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vec := range vectors {
		got, given, err := jsbind.QueryText(vec.Query, "name")
		switch vec.Kind {
		case "undefined":
			if given || err != nil {
				t.Errorf("QueryText(%q) = %q, %v, %v; want no parameter", vec.Query, got, given, err)
			}
		case "string":
			var want string
			_ = json.Unmarshal(vec.Value, &want)
			if got != want || !given || err != nil {
				t.Errorf("QueryText(%q) = %q, %v, %v; want the text %q", vec.Query, got, given, err, want)
			}
		default:
			if !given || !errors.Is(err, jsbind.ErrNotOneText) {
				t.Errorf("QueryText(%q) = %q, %v, %v; want no single text (qs: %s)", vec.Query, got, given, err, vec.Kind)
			}
		}
	}
}

// Whatever the query, QueryText agrees with the parser of goapipath,
// which follows qs step by step: the same text where that parser made a
// text, and "no single text" where it made a list or an object. Whether
// goapipath made a text shows in what a later name[zq]=y does to it: a
// text becomes the list [text, {zq:"y"}], a list or an object becomes an
// object.
func FuzzQueryText(f *testing.F) {
	for _, seed := range []string{"name=a", "name=a&name=b", "name[]=a", "[name]=x", "name=a&[name]=", "name[=1", "name%5B0%5D=1", "a=1#name=2", "name=%E2%9C%93&x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		if strings.Contains(query, "zq") || strings.Count(query, "&") > 990 {
			return
		}
		got, given, err := jsbind.QueryText(query, "name")
		value := goapipath.QueryOf(query, "name")
		if given != value.Exists() {
			t.Fatalf("QueryText(%q) given %v, goapipath %v", query, given, value.Exists())
		}
		if !given {
			return
		}
		valueText, valueErr := value.Text()
		before, after, _ := strings.Cut(query, "#")
		probe, probeErr := goapipath.QueryOf(before+"&name[zq]=y#"+after, "name").Text()
		isText := valueErr == nil && probeErr == nil && probe == valueText+",[object Object]"
		if isText != (err == nil) {
			t.Fatalf("QueryText(%q) one text %v, goapipath %v (%q, probe %q)", query, err == nil, isText, valueText, probe)
		}
		if err == nil && got != valueText {
			t.Fatalf("QueryText(%q) = %q, goapipath %q", query, got, valueText)
		}
	})
}
