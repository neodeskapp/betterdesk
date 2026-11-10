package contract

import (
	"reflect"
	"strings"
	"testing"
)

func golden(a Answer, volatile ...string) GoldenAnswer {
	return newGoldenAnswer(a, plainMarks(volatile), nil)
}

func diffStrings(diffs []Diff) []string {
	out := make([]string, len(diffs))
	for i, d := range diffs {
		out[i] = d.String()
	}
	return out
}

func TestCompareAnswersEqual(t *testing.T) {
	want := jsonAnswer(200, `{"a": 1, "b": [1, 2, {"c": null}], "d": "x", "n": 1.0, "big": 12345678901234567890}`)
	got := jsonAnswer(200, `{"d": "x", "b": [1, 2, {"c": null}], "a": 1, "n": 1, "big": 12345678901234567890}`)
	if diffs := CompareAnswers(golden(want), got); len(diffs) > 0 {
		t.Errorf("equal answers differ: %q", diffStrings(diffs))
	}
}

func TestCompareAnswersFindsEachKindOfDifference(t *testing.T) {
	want := jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": "x", "tags": ["a", "b"], "note": null}}`)
	cases := []struct {
		name string
		got  Answer
		diff string
	}{
		{"status", jsonAnswer(404, `{"success": true, "data": {"id": 7, "name": "x", "tags": ["a", "b"], "note": null}}`),
			"status: expected 200, got 404"},
		{"value", jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": "y", "tags": ["a", "b"], "note": null}}`),
			`body data.name: expected "x", got "y"`},
		{"number", jsonAnswer(200, `{"success": true, "data": {"id": 8, "name": "x", "tags": ["a", "b"], "note": null}}`),
			"body data.id: expected 7, got 8"},
		{"big number", jsonAnswer(200, `{"success": true, "data": {"id": 7.0000000000000001, "name": "x", "tags": ["a", "b"], "note": null}}`),
			"body data.id: expected 7, got 7.0000000000000001"},
		{"missing key", jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": "x", "tags": ["a", "b"]}}`),
			`body data: missing key "note"`},
		{"extra key", jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": "x", "tags": ["a", "b"], "note": null, "more": 1}}`),
			`body data: unexpected key "more" with value 1`},
		{"type", jsonAnswer(200, `{"success": true, "data": {"id": "7", "name": "x", "tags": ["a", "b"], "note": null}}`),
			`body data.id: type: expected number 7, got string "7"`},
		{"null is not missing", jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": null, "tags": ["a", "b"], "note": null}}`),
			`body data.name: type: expected string "x", got null null`},
		{"array order", jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": "x", "tags": ["b", "a"], "note": null}}`),
			`body data.tags[0]: expected "a", got "b"`},
		{"array length", jsonAnswer(200, `{"success": true, "data": {"id": 7, "name": "x", "tags": ["a", "b", "c"], "note": null}}`),
			"body data.tags: array length: expected 2, got 3"},
		{"bool", jsonAnswer(200, `{"success": false, "data": {"id": 7, "name": "x", "tags": ["a", "b"], "note": null}}`),
			"body success: expected true, got false"},
	}
	for _, c := range cases {
		diffs := diffStrings(CompareAnswers(golden(want), c.got))
		if len(diffs) == 0 || diffs[0] != c.diff {
			t.Errorf("%s: diffs %q, want first %q", c.name, diffs, c.diff)
		}
	}
}

func TestCompareHeaders(t *testing.T) {
	want := jsonAnswer(200, `{}`)
	want.Headers = map[string][]string{
		"content-type":  {"application/json; charset=utf-8"},
		"cache-control": {"no-store"},
		"set-cookie":    {"a=<value>; Path=/", "b=<value>; Path=/"},
		"etag":          {"<present>"},
	}
	got := jsonAnswer(200, `{}`)
	got.Headers = map[string][]string{
		"content-type": {"application/json"},
		"set-cookie":   {"a=<value>; Path=/"},
		"etag":         {"<present>"},
		"vary":         {"Origin"},
	}
	diffs := diffStrings(CompareAnswers(golden(want), got))
	wantDiffs := []string{
		`header cache-control: missing: expected "no-store", got no header`,
		`header content-type: expected "application/json; charset=utf-8", got "application/json"`,
		`header set-cookie: expected "a=<value>; Path=/", "b=<value>; Path=/", got "a=<value>; Path=/"`,
		`header vary: unexpected: expected no header, got "Origin"`,
	}
	if !reflect.DeepEqual(diffs, wantDiffs) {
		t.Errorf("header diffs:\n got %q\nwant %q", diffs, wantDiffs)
	}

	// A header marked volatile has to be there, with any value.
	g := golden(want)
	g.VolatileHeaders = []string{"Content-Type", "cache-control"}
	diffs = diffStrings(CompareAnswers(g, got))
	if len(diffs) != 3 || !strings.HasPrefix(diffs[0], "header cache-control: missing") || strings.Contains(strings.Join(diffs, "\n"), "content-type") {
		t.Errorf("with volatile headers: %q, want cache-control still missing and content-type accepted", diffs)
	}
}

func TestCompareVolatileChecksTheTypeOnly(t *testing.T) {
	want := jsonAnswer(200, `{"token": "<volatile>", "count": 0, "items": [{"id": 0, "name": "x"}, {"id": 0, "name": "y"}], "log": [{}], "sessions": {}}`)
	g := golden(want, "token", "count", "items[*].id", "log[#]", "sessions")

	got := jsonAnswer(200, `{"token": "abc", "count": 17, "items": [{"id": 5, "name": "x"}, {"id": 6, "name": "y"}], "log": [{"a": 1}, {"b": 2}, {}], "sessions": {"k": {"deep": true}}}`)
	if diffs := CompareAnswers(g, got); len(diffs) > 0 {
		t.Errorf("volatile values differ: %q", diffStrings(diffs))
	}

	got = jsonAnswer(200, `{"token": 5, "count": "17", "items": [{"id": null, "name": "z"}, {"id": 6, "name": "y"}], "log": [{"a": 1}, "text"], "sessions": []}`)
	diffs := diffStrings(CompareAnswers(g, got))
	wantDiffs := []string{
		`body count: type: expected number 0, got string "17"`,
		`body items[0].id: type: expected number 0, got null null`,
		`body items[0].name: expected "x", got "z"`,
		`body log[1]: type: expected object, got string "text"`,
		`body sessions: type: expected object {}, got array []`,
		`body token: type: expected string "<volatile>", got number`,
	}
	if !reflect.DeepEqual(diffs, wantDiffs) {
		t.Errorf("diffs:\n got %q\nwant %q", diffs, wantDiffs)
	}

	// Without the length mark the array must have the golden's length.
	g = golden(want, "token", "count", "items[*].id", "sessions")
	got = jsonAnswer(200, `{"token": "abc", "count": 17, "items": [{"id": 5, "name": "x"}, {"id": 6, "name": "y"}], "log": [{}, {}], "sessions": {}}`)
	diffs = diffStrings(CompareAnswers(g, got))
	if !reflect.DeepEqual(diffs, []string{"body log: array length: expected 1, got 2"}) {
		t.Errorf("diffs without the length mark: %q", diffs)
	}
}

func TestCompareBodiesThatAreNotJSON(t *testing.T) {
	page := Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html", Length: 5, SHA256: "aaaaaaaaaaaaaaaa", Head: "<a>"}}
	other := Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html", Length: 6, SHA256: "bbbbbbbbbbbbbbbb", Head: "<b>"}}

	if diffs := CompareAnswers(golden(page), page); len(diffs) > 0 {
		t.Errorf("the same page differs: %q", diffStrings(diffs))
	}
	diffs := diffStrings(CompareAnswers(golden(page), other))
	want := `body: content: expected 5 bytes, sha256 aaaaaaaaaaaa, starting "<a>", got 6 bytes, sha256 bbbbbbbbbbbb, starting "<b>"`
	if len(diffs) != 1 || diffs[0] != want {
		t.Errorf("diffs %q, want %q", diffs, want)
	}
	if diffs := CompareAnswers(golden(Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html"}}, "$"), other); len(diffs) > 0 {
		t.Errorf("a volatile page differs: %q", diffStrings(diffs))
	}
	plain := other
	plain.Body.Type = "text/plain"
	if diffs := CompareAnswers(golden(Answer{Status: 200, Body: Body{Kind: BodyBytes, Type: "text/html"}}, "$"), plain); len(diffs) != 1 {
		t.Errorf("a volatile page of another media type: %q, want one difference", diffStrings(diffs))
	}

	diffs = diffStrings(CompareAnswers(golden(page), jsonAnswer(200, `{"a": 1}`)))
	if len(diffs) != 2 || diffs[1] != `body: kind: expected text/html, 5 bytes, got JSON {"a":1}` {
		t.Errorf("page against JSON: %q", diffs)
	}
	empty := Answer{Status: 204, Body: Body{Kind: BodyEmpty}}
	if diffs := CompareAnswers(golden(empty), empty); len(diffs) > 0 {
		t.Errorf("two empty answers differ: %q", diffStrings(diffs))
	}
}

func TestCompareFailedAndRedirects(t *testing.T) {
	ok := jsonAnswer(200, `{}`)
	ok.Redirects = []string{"/login"}
	timeout := Answer{Failed: "timeout", Body: Body{Kind: BodyEmpty}}
	diffs := diffStrings(CompareAnswers(golden(ok), timeout))
	if len(diffs) < 3 || diffs[0] != "answer: expected an answer, got no answer (timeout)" || diffs[2] != `redirects: expected "/login", got none` {
		t.Errorf("diffs %q", diffs)
	}
	if diffs := CompareAnswers(golden(timeout), timeout); len(diffs) > 0 {
		t.Errorf("two timeouts differ: %q", diffStrings(diffs))
	}
}

func TestFormatMismatch(t *testing.T) {
	lines := make([]string, maxDiffLines+5)
	for i := range lines {
		lines[i] = "body x: expected 1, got 2"
	}
	text := formatMismatch("GET /api/devices/:id", "viewer", "testdata/golden/matrix/devices/GET_api_devices__id.json", lines)
	if !strings.HasPrefix(text, "GET /api/devices/:id as viewer differs from testdata/golden/matrix/devices/GET_api_devices__id.json\n    body x: expected 1, got 2\n") {
		t.Errorf("mismatch text starts with %q", text[:120])
	}
	if !strings.HasSuffix(text, "\n    ... and 5 more") || strings.Count(text, "body x") != maxDiffLines {
		t.Errorf("mismatch text does not stop after %d differences: ...%q", maxDiffLines, text[len(text)-40:])
	}
}
