package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// recordedAt is the time the test recordings were made.
var recordedAt = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func jsonAnswer(status int, body string) Answer {
	value, err := decodeJSON([]byte(body))
	if err != nil {
		panic(err)
	}
	return Answer{
		Status:  status,
		Headers: map[string][]string{"content-type": {"application/json; charset=utf-8"}},
		Body:    Body{Kind: BodyJSON, JSON: encodeJSON(value)},
	}
}

func TestPathRoundTrip(t *testing.T) {
	for _, text := range []string{
		"$", "data", "data.items", "data.items[2].id", "data.items[*].id", "data.items[#]",
		"[*].id", "[0]", `data["odd key"].x`, `["en.json"]`, `a["with ] bracket"][3]`, `["900100001"].name`, "a-b.c_d",
	} {
		p, err := parsePath(text)
		if err != nil {
			t.Errorf("parsePath(%q): %v", text, err)
			continue
		}
		if got := p.String(); got != text {
			t.Errorf("parsePath(%q).String() = %q", text, got)
		}
	}
	for _, text := range []string{"", ".a", "a..b", "a[", "a[x]", "a[-1]", "a[#].b", `a["x"`, "a[1]b"} {
		if p, err := parsePath(text); err == nil {
			t.Errorf("parsePath(%q) = %v, want an error", text, p)
		}
	}
}

func TestLookup(t *testing.T) {
	value, _ := decodeJSON([]byte(`{"data":{"id":42,"items":[{"id":"a"},{"id":"b"}],"odd key":null}}`))
	cases := []struct {
		path string
		want any
		ok   bool
	}{
		{"data.id", json.Number("42"), true},
		{"data.items[1].id", "b", true},
		{`data["odd key"]`, nil, true},
		{"data.items[2].id", nil, false},
		{"data.missing", nil, false},
		{"data.id.deeper", nil, false},
		{"data.items[*].id", nil, false},
	}
	for _, c := range cases {
		p, err := parsePath(c.path)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := lookup(value, p)
		if ok != c.ok || got != c.want {
			t.Errorf("lookup(%s) = %v, %v; want %v, %v", c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestDetectVolatileValues(t *testing.T) {
	first := jsonAnswer(200, `{
		"success": true, "token": "aaa", "count": 3, "online": true,
		"same": {"deep": [1, 2, 3]},
		"items": [{"id": 1, "created": "t1", "name": "x"}, {"id": 2, "created": "t2", "name": "y"}],
		"some": [{"seen": "a", "name": "x"}, {"seen": "b", "name": "y"}, {"seen": "c", "name": "z"}],
		"log": ["a", "b"],
		"odd key": {"v": 1}
	}`)
	second := jsonAnswer(200, `{
		"success": true, "token": "bbb", "count": 4, "online": false,
		"same": {"deep": [1, 2, 3]},
		"items": [{"id": 7, "created": "t3", "name": "x"}, {"id": 2, "created": "t4", "name": "y"}],
		"some": [{"seen": "a", "name": "x"}, {"seen": "B", "name": "y"}, {"seen": "c", "name": "z"}],
		"log": ["a", "b", "c"],
		"odd key": {"v": 2}
	}`)
	found := detectVolatile(first, second, nil, nil, recordedAt)
	if len(found.Problems) > 0 {
		t.Fatalf("problems: %v", found.Problems)
	}
	want := []string{
		`["odd key"].v`,
		"count",
		"items[*].created",
		"items[0].id",
		"log[#]",
		"log[*]",
		"online",
		"some[1].seen",
		"token",
	}
	if !reflect.DeepEqual(found.Volatile, want) {
		t.Errorf("volatile:\n got %q\nwant %q", found.Volatile, want)
	}
}

func TestDetectVolatileSkipsWhatIsKnown(t *testing.T) {
	first := jsonAnswer(200, `{"sessions": {"abc": 1}, "list": [1, 1], "x": "a", "v": null}`)
	second := jsonAnswer(200, `{"sessions": {"xyz": 1, "q": 2}, "list": [1], "x": "b", "v": null}`)
	found := detectVolatile(first, second, []string{"sessions", "list[#]"}, nil, recordedAt)
	if len(found.Problems) > 0 || !reflect.DeepEqual(found.Volatile, []string{"x"}) {
		t.Errorf("found %+v, want only x and no problems: sessions and the length of list are marked by hand, and the elements of list are all 1", found)
	}
}

func TestDetectVolatileReportsWhatIsNotAValue(t *testing.T) {
	base := jsonAnswer(200, `{"a": 1, "b": {"c": "x"}, "d": [1]}`)
	cases := []struct {
		name   string
		second Answer
		want   string
	}{
		{"status", jsonAnswer(500, `{"a": 1, "b": {"c": "x"}, "d": [1]}`), "status: 200 in the first recording, 500 in the second"},
		{"type", jsonAnswer(200, `{"a": "1", "b": {"c": "x"}, "d": [1]}`), `body a: number 1 in the first recording, string "1" in the second`},
		{"null", jsonAnswer(200, `{"a": 1, "b": {"c": null}, "d": [1]}`), `body b.c: string "x" in the first recording, null null in the second`},
		{"keys", jsonAnswer(200, `{"a": 1, "b": {"e": "x"}, "d": [1]}`), `body b: keys differ: only in the first recording "c", only in the second "e"`},
		{"element type", jsonAnswer(200, `{"a": 1, "b": {"c": "x"}, "d": [[1]]}`), "body d[0]: number 1 in the first recording, array [1] in the second"},
	}
	for _, c := range cases {
		found := detectVolatile(base, c.second, nil, nil, recordedAt)
		if len(found.Problems) != 1 || found.Problems[0] != c.want {
			t.Errorf("%s: problems %q, want %q", c.name, found.Problems, c.want)
		}
	}

	other := jsonAnswer(200, `{"a": 1, "b": {"c": "x"}, "d": [1]}`)
	other.Headers = map[string][]string{"content-type": {"text/plain"}, "location": {"/x"}}
	found := detectVolatile(base, other, nil, nil, recordedAt)
	if len(found.Problems) != 2 || !strings.Contains(found.Problems[0], "header content-type") || !strings.Contains(found.Problems[1], "header location") {
		t.Errorf("header problems = %q", found.Problems)
	}
	found = detectVolatile(base, other, nil, []string{"Content-Type"}, recordedAt)
	if len(found.Problems) != 1 || !strings.Contains(found.Problems[0], "header location") {
		t.Errorf("with content-type marked by hand: problems = %q, want only location", found.Problems)
	}

	html := Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html", Length: 5, SHA256: "aa"}}
	if found := detectVolatile(base, html, nil, nil, recordedAt); len(found.Problems) == 0 {
		t.Error("a JSON body in one recording and HTML in the other is not reported")
	}
}

func TestDetectVolatileBytes(t *testing.T) {
	first := Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html", Length: 5, SHA256: "aa", Head: "<a>"}}
	second := Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html", Length: 6, SHA256: "bb", Head: "<b>"}}
	found := detectVolatile(first, second, nil, nil, recordedAt)
	if len(found.Problems) > 0 || !reflect.DeepEqual(found.Volatile, []string{"$"}) {
		t.Errorf("found %+v, want the whole body volatile", found)
	}
	if found := detectVolatile(first, first, nil, nil, recordedAt); len(found.Volatile) > 0 || len(found.Problems) > 0 {
		t.Errorf("the same page twice: %+v, want nothing", found)
	}
	second.Body.Type = "text/plain"
	if found := detectVolatile(first, second, nil, nil, recordedAt); len(found.Problems) != 1 {
		t.Errorf("another media type: %+v, want one problem", found)
	}
}

func TestTimeBound(t *testing.T) {
	now := recordedAt
	bound := []any{
		"2026-11-10",
		"2026-11-10T00:00:00.000Z",
		"backup-2026-11-09.zip",
		"valid until 2026-12-20",
		"Tue, 10 Nov 2026 00:00:00 GMT",
		"Nov 10, 2026",
		"10 November 2026",
		json.Number("1794268800"),    // 2026-11-10 00:00:00 UTC in seconds
		json.Number("1794268800000"), // the same in milliseconds
	}
	for _, v := range bound {
		if !timeBound(v, now) {
			t.Errorf("timeBound(%v) = false, want true", v)
		}
	}
	free := []any{
		"2025-01-01",
		"2027-06-01T00:00:00Z",
		"version 3.5.224",
		"900100001",
		"1 Jan 2020",
		json.Number("900100001"),
		json.Number("42"),
		json.Number("1794268800.5"),
		true,
		nil,
	}
	for _, v := range free {
		if timeBound(v, now) {
			t.Errorf("timeBound(%v) = true, want false", v)
		}
	}
}

func TestDetectVolatileMarksEqualValuesBoundToToday(t *testing.T) {
	a := jsonAnswer(200, `{"day": "2026-11-10", "since": 1794268800, "fixed": "2020-01-01", "n": 5}`)
	found := detectVolatile(a, a, nil, nil, recordedAt)
	if !reflect.DeepEqual(found.Volatile, []string{"day", "since"}) || found.TimeBound != 2 {
		t.Errorf("found %+v, want day and since marked although both recordings agree", found)
	}
}

func TestMaskVolatile(t *testing.T) {
	a := jsonAnswer(200, `{
		"token": "secret-of-this-instance", "count": 3, "online": true, "keep": "x", "nothing": null,
		"items": [{"id": 1, "created": "t1"}, {"id": 2, "created": "t2"}],
		"log": [{"line": "a"}, {"line": "b"}],
		"empty": [],
		"sessions": {"abc": 1}
	}`)
	masked, err := maskVolatile(a, []string{"token", "count", "online", "nothing", "items[*].created", "items[1].id", "log[#]", "empty[#]", "sessions"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":0,"empty":[],"items":[{"created":"<volatile>","id":1},{"created":"<volatile>","id":0}],"keep":"x","log":[{"line":"a"}],"nothing":null,"online":false,"sessions":{},"token":"<volatile>"}`
	if got := string(masked.Body.JSON); got != want {
		t.Errorf("masked body:\n got %s\nwant %s", got, want)
	}

	page := Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html", Length: 5, SHA256: "aa", Head: "<a>"}}
	masked, err = maskVolatile(page, []string{"$"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Body{Kind: BodyBytes, Type: "text/html"}); !reflect.DeepEqual(masked.Body, want) {
		t.Errorf("masked page = %+v, want only kind and type left", masked.Body)
	}
	if unmasked, _ := maskVolatile(page, nil); !reflect.DeepEqual(unmasked.Body, page.Body) {
		t.Errorf("a page without marks changed: %+v", unmasked.Body)
	}
}
