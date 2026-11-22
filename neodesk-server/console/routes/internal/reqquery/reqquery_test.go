package reqquery

import (
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

func of(rawQuery string) *Query {
	r := httptest.NewRequest("GET", "http://console.test/x", nil)
	r.URL.RawQuery = rawQuery
	return Of(r)
}

// The parser itself is the one of console/routes/meshcentral, which has
// the vectors for it; the route groups that use this package replay
// thousands of queries through it (cdap, guest). These tests cover what
// this package adds around the parser.

func TestSearchWritesTheQueryOutAgain(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"a=1&b=2":                "a=1&b=2",
		"b=2&a=1&5=five&1=one":   "1=one&5=five&b=2&a=1",
		"a=1&a=2":                "a=1%2C2",
		"a[]=x&a[]=y":            "a=x%2Cy",
		"a[k]=v":                 "a=%5Bobject+Object%5D",
		"a=b%20c%26d":            "a=b+c%26d",
		"a":                      "a=",
		"a=%E4%B8%96":            "a=%E4%B8%96",
		"t=tok&view_only=0#frag": "t=tok&view_only=0",
	}
	for query, want := range cases {
		if got, ok := of(query).Search(); !ok || got != want {
			t.Errorf("Search(%q) = %q, %v; want %q", query, got, ok, want)
		}
	}
	if _, ok := of("a[toString]=1").Search(); ok {
		t.Error("a value without a text was written out")
	}
}

func TestSetDeleteHas(t *testing.T) {
	q := of("t=tok&x=1&view_only=0")
	if !q.Has("t") || q.Has("guest") {
		t.Fatal("Has is wrong before the changes")
	}
	q.Delete("t")
	q.Delete("nothing")
	q.Set("guest", "tok")
	q.Set("view_only", "1")
	if q.Has("t") || !q.Has("guest") {
		t.Error("Has is wrong after the changes")
	}
	if got, _ := q.Search(); got != "x=1&view_only=1&guest=tok" {
		t.Errorf("after the changes: %q", got)
	}
}

func TestValues(t *testing.T) {
	q := of("s=text&e=&l=a&l=b&o[k]=v&o[n][]=1&bad[toString]=x")
	if v := q.Get("missing"); v.Defined() || v.Truthy() || v.JSON() != nil {
		t.Error("a missing parameter has a value")
	}
	if text, ok := q.Get("missing").Text(); !ok || text != "undefined" {
		t.Errorf("text of a missing parameter: %q, %v", text, ok)
	}
	if v := q.Get("e"); !v.Defined() || v.Truthy() || v.JSON() != "" {
		t.Error("an empty parameter")
	}
	if text, isText := q.Get("s").IsText(); !isText || text != "text" || q.Get("s").JSON() != "text" {
		t.Error("a text parameter")
	}
	list, isList := q.Get("l").JSON().([]any)
	if _, isText := q.Get("l").IsText(); isText || !isList || len(list) != 2 || list[1] != "b" {
		t.Errorf("a repeated parameter: %v", q.Get("l").JSON())
	}
	if text, ok := q.Get("l").Text(); !ok || text != "a,b" {
		t.Errorf("text of a list: %q", text)
	}
	object, isObject := q.Get("o").JSON().(*goapi.Object)
	if !isObject || object.Get("k") != "v" {
		t.Fatalf("a bracketed parameter: %v", q.Get("o").JSON())
	}
	if inner, isList := object.Get("n").([]any); !isList || len(inner) != 1 || inner[0] != "1" {
		t.Errorf("a list inside an object: %v", object.Get("n"))
	}
	if _, ok := q.Get("bad").Text(); ok {
		t.Error("an object with a toString member has a text")
	}
	if !q.Get("bad").Truthy() {
		t.Error("an object is not truthy")
	}
}
