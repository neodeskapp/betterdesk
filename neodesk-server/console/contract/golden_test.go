package contract

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGoldenRoundTrip(t *testing.T) {
	page := Answer{
		Status:  200,
		Headers: map[string][]string{"content-type": {"text/html; charset=utf-8"}, "set-cookie": {"a=<value>; Path=/"}},
		Body:    Body{Kind: BodyBytes, Type: "text/html", Length: 12, SHA256: "ab", Head: "<!doctype html> <b>&amp;</b>"},
	}
	refused := jsonAnswer(403, `{"success": false, "error": "Forbidden <b>", "n": 12345678901234567890, "f": 1.50, "nothing": null, "list": []}`)
	redirect := Answer{Status: 302, Redirects: []string{"/a"}, Headers: map[string][]string{"location": {"/login"}}, Body: Body{Kind: BodyEmpty}}
	timeout := Answer{Failed: "timeout", Body: Body{Kind: BodyEmpty}}

	g := &Golden{
		ID:         "GET /api/devices/:id",
		Recordings: 2,
		Request:    RequestInfo{Method: "GET", Path: "/api/devices/999999999", Notes: []string{"no CSRF token"}},
		Answers: []GoldenAnswer{
			{Roles: []Role{SignedOut, "pro"}, Status: redirect.Status, Redirects: redirect.Redirects, Headers: redirect.Headers, Body: redirect.Body},
			{Roles: []Role{"viewer"}, Status: refused.Status, Headers: refused.Headers, Body: refused.Body, Volatile: []VolatileMark{{Path: "error", Format: "Aaaaaaaaa <a>"}, {Path: "list[#]"}, {Path: "n", Integer: true}, {Path: "f", Pattern: `^9+\.9+$`}}, VolatileHeaders: []string{"content-type"}},
			{Roles: []Role{"operator"}, Status: page.Status, Headers: page.Headers, Body: page.Body},
			{Roles: []Role{"admin"}, Failed: timeout.Failed, Body: timeout.Body},
		},
	}
	file := filepath.Join(t.TempDir(), "matrix", "devices", "GET_api_devices__id.json")
	if err := g.Save(file); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGolden(file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(contentOf(g), contentOf(loaded)) {
		t.Errorf("the golden changed on its way through the file:\n got %+v\nwant %+v", contentOf(loaded), contentOf(g))
	}
	for i, answer := range g.Answers {
		if diffs := CompareAnswers(loaded.Answers[i], answer.Answer()); len(diffs) > 0 {
			t.Errorf("answer %d does not compare equal to itself after the round trip: %q", i, diffStrings(diffs))
		}
	}

	first, _ := os.ReadFile(file)
	if err := loaded.Save(file); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(file)
	if string(first) != string(second) {
		t.Error("saving the loaded golden gives other bytes")
	}
	text := string(first)
	for _, want := range []string{
		`"content-type": "text/html; charset=utf-8"`, `"Forbidden <b>"`, `12345678901234567890`, `1.50`, `"<!doctype html> <b>&amp;</b>"`, `"nothing": null`, `"recordings": 2`,
		`"list[#]",`, `"path": "error"`, `"format": "Aaaaaaaaa <a>"`, `"integer": true`, `"pattern": "^9+\\.9+$"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the file does not hold %s as it was:\n%s", want, text)
		}
	}

	if _, ok := loaded.For("viewer"); !ok {
		t.Error("For(viewer) found nothing")
	}
	if a, ok := loaded.For("pro"); !ok || a.Status != 302 {
		t.Errorf("For(pro) = %+v, %v", a, ok)
	}
	if _, ok := loaded.For("server_admin"); ok {
		t.Error("For(server_admin) found an answer nobody recorded")
	}
}

// contentOf gives a golden's content with the JSON bodies decoded, so two
// goldens compare by value.
func contentOf(g *Golden) []any {
	out := []any{g.ID, g.Request, g.Recordings}
	for _, a := range g.Answers {
		var body any
		if a.Body.Kind == BodyJSON {
			body, _ = decodeJSON(a.Body.JSON)
		}
		meta := a.Body
		meta.JSON = nil
		out = append(out, []any{a.Roles, a.Status, a.Failed, a.Redirects, a.Headers, a.Volatile, a.VolatileHeaders, meta, body})
	}
	return out
}

func TestLoadGoldenRefusesUnknownFieldsAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadGolden(filepath.Join(dir, "missing.json")); !isNotExist(err) {
		t.Errorf("a missing golden gives %v, want a not-exist error", err)
	}
	file := filepath.Join(dir, "typo.json")
	if err := os.WriteFile(file, []byte(`{"id":"x","request":{"method":"GET","path":"/"},"answers":[{"roles":["admin"],"status":200,"volatil":["a"],"body":{"kind":"empty"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGolden(file); err == nil {
		t.Error("a golden with a misspelt field was accepted")
	}
}

func TestBuildStoresEqualAnswersOnce(t *testing.T) {
	refused := jsonAnswer(403, `{"success": false}`)
	rec := recording{
		ID:      "GET /api/x",
		Request: RequestInfo{Method: "GET", Path: "/api/x"},
		Answers: map[Role]Answer{
			"admin":       jsonAnswer(200, `{"success": true}`),
			"viewer":      refused,
			SignedOut:     jsonAnswer(401, `{"success": false}`),
			"operator":    refused,
			"pro":         jsonAnswer(401, `{"success": false}`),
			"super_admin": jsonAnswer(200, `{"success": true}`),
		},
		Volatile: []string{"z", "a"},
	}
	g := rec.build()
	var got [][]Role
	for _, a := range g.Answers {
		got = append(got, a.Roles)
		if !reflect.DeepEqual(a.Volatile, []VolatileMark{{Path: "a"}, {Path: "z"}}) {
			t.Errorf("marks set by hand = %+v, want them on every answer, sorted", a.Volatile)
		}
	}
	want := [][]Role{{SignedOut, "pro"}, {"viewer", "operator"}, {"super_admin", "admin"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roles per answer = %v, want %v (least privilege first)", got, want)
	}
}

func TestMergeMarksAndMasksWhatDiffers(t *testing.T) {
	first := recording{
		ID: "GET /api/x", Request: RequestInfo{Method: "GET", Path: "/api/x"},
		Answers: map[Role]Answer{
			SignedOut: jsonAnswer(401, `{"error": "Unauthorized"}`),
			"viewer":  jsonAnswer(200, `{"id": 11, "at": "t1", "name": "x"}`),
			"admin":   jsonAnswer(200, `{"id": 12, "at": "t2", "name": "x"}`),
		},
	}
	second := recording{
		ID: "GET /api/x", Request: RequestInfo{Method: "GET", Path: "/api/x"},
		Answers: map[Role]Answer{
			SignedOut: jsonAnswer(401, `{"error": "Unauthorized"}`),
			"viewer":  jsonAnswer(200, `{"id": 21, "at": "t3", "name": "x"}`),
			"admin":   jsonAnswer(200, `{"id": 22, "at": "t4", "name": "x"}`),
		},
	}
	firstGolden := first.build()
	if firstGolden.Recordings != 1 {
		t.Errorf("a first recording says it was made from %d recordings", firstGolden.Recordings)
	}
	if len(firstGolden.Answers) != 3 {
		t.Fatalf("first recording has %d answers, want 3: viewer and admin differ in id", len(firstGolden.Answers))
	}
	merged, problems, _ := second.merge(firstGolden, recordedAt)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	if len(merged.Answers) != 2 {
		t.Fatalf("merged golden has %d answers, want 2: viewer and admin are the same once the volatile values are masked", len(merged.Answers))
	}
	shared := merged.Answers[1]
	wantMarks := []VolatileMark{{Path: "at", Format: "a9"}, {Path: "id", Integer: true}}
	if !reflect.DeepEqual(shared.Roles, []Role{"viewer", "admin"}) || !reflect.DeepEqual(shared.Volatile, wantMarks) || merged.Recordings != 2 {
		t.Errorf("shared answer: roles %v, volatile %+v, recordings %d", shared.Roles, shared.Volatile, merged.Recordings)
	}
	if got, want := string(shared.Body.JSON), `{"at":"<volatile>","id":0,"name":"x"}`; got != want {
		t.Errorf("shared body = %s, want %s", got, want)
	}

	// A third instance compares clean against the merged golden, and a
	// change in a stable value is still found.
	third := jsonAnswer(200, `{"id": 31, "at": "t5", "name": "x"}`)
	want, _ := merged.For("admin")
	if diffs := CompareAnswers(want, third); len(diffs) > 0 {
		t.Errorf("a third instance differs: %q", diffStrings(diffs))
	}
	changed := jsonAnswer(200, `{"id": 31, "at": "t5", "name": "y"}`)
	if diffs := CompareAnswers(want, changed); len(diffs) != 1 {
		t.Errorf("a changed name gives %q, want one difference", diffStrings(diffs))
	}
	reformatted := jsonAnswer(200, `{"id": 31.5, "at": "5t", "name": "x"}`)
	if diffs := CompareAnswers(want, reformatted); len(diffs) != 2 {
		t.Errorf("volatile values in another format give %q, want two differences", diffStrings(diffs))
	}

	// Merging once more keeps the answers and their marks: the values are
	// masked now, and "<volatile>" must not become the format.
	again, problems, _ := second.merge(merged, recordedAt)
	if len(problems) > 0 || again.Recordings != 3 || !reflect.DeepEqual(contentOf(again)[3:], contentOf(merged)[3:]) {
		t.Errorf("a second merge changed the golden or found problems: %q\n got %+v\nwant %+v", problems, contentOf(again), contentOf(merged))
	}
}

func TestMergeDerivesFormatsFromAllRolesWithTheSameStatus(t *testing.T) {
	first := recording{ID: "GET /api/x", Answers: map[Role]Answer{
		"viewer":  jsonAnswer(200, `{"uptime": "58.1s", "who": "viewer"}`),
		"admin":   jsonAnswer(200, `{"uptime": "101.25s", "who": "admin"}`),
		SignedOut: jsonAnswer(401, `{"uptime": "never, not signed in"}`),
	}}
	second := recording{ID: "GET /api/x", Answers: map[Role]Answer{
		"viewer":  jsonAnswer(200, `{"uptime": "59.3s", "who": "viewer"}`),
		"admin":   jsonAnswer(200, `{"uptime": "102.75s", "who": "admin"}`),
		SignedOut: jsonAnswer(401, `{"uptime": "never, not signed in"}`),
	}}
	merged, problems, _ := second.merge(first.build(), recordedAt)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	for _, role := range []Role{"viewer", "admin"} {
		answer, _ := merged.For(role)
		want := []VolatileMark{{Path: "uptime", Pattern: `^9+\.9+a$`}}
		if !reflect.DeepEqual(answer.Volatile, want) {
			t.Errorf("%s: marks %+v, want %+v: the four values of the two roles differ in the length of their runs", role, answer.Volatile, want)
		}
	}
	if out, _ := merged.For(SignedOut); len(out.Volatile) != 0 {
		t.Errorf("signed out: marks %+v, want none", out.Volatile)
	}
}

func TestMarksSetByHand(t *testing.T) {
	first := recording{ID: "GET /api/x", Volatile: []string{"~up", "stratum", "at"}, Answers: map[Role]Answer{
		"admin": jsonAnswer(200, `{"up": "51.3s", "stratum": 2, "at": "2026-11-10 01:02:03", "name": "x"}`),
	}}
	second := first
	second.Answers = map[Role]Answer{
		"admin": jsonAnswer(200, `{"up": "52.9s", "stratum": 2, "at": "2026-11-10 01:02:03", "name": "x"}`),
	}
	firstGolden := first.build()
	if got := firstGolden.Answers[0].Volatile; !reflect.DeepEqual(got, []VolatileMark{{Path: "at"}, {Path: "stratum"}, {Path: "up"}}) {
		t.Errorf("first recording: marks %+v, want the three paths without the ~", got)
	}
	merged, problems, _ := second.merge(firstGolden, recordedAt)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	// A path marked by hand is volatile although both recordings agree on
	// it, and keeps its format unless it starts with ~.
	want := []VolatileMark{{Path: "at", Format: "9999-99-99 99:99:99"}, {Path: "stratum", Integer: true}, {Path: "up"}}
	answer, _ := merged.For("admin")
	if !reflect.DeepEqual(answer.Volatile, want) {
		t.Errorf("marks %+v, want %+v", answer.Volatile, want)
	}
	later := jsonAnswer(200, `{"up": "1m3.2s", "stratum": 4, "at": "2027-01-01 00:00:00", "name": "x"}`)
	if diffs := CompareAnswers(answer, later); len(diffs) > 0 {
		t.Errorf("a later answer differs: %q", diffStrings(diffs))
	}
}

func TestMergeReportsProblemsWithTheRole(t *testing.T) {
	first := recording{ID: "GET /api/x", Answers: map[Role]Answer{
		"viewer": jsonAnswer(200, `{"a": 1}`),
		"admin":  jsonAnswer(200, `{"a": 1}`),
	}}
	second := recording{ID: "GET /api/x", Answers: map[Role]Answer{
		"viewer":   jsonAnswer(200, `{"a": 1}`),
		"admin":    jsonAnswer(500, `{"a": "1"}`),
		"operator": jsonAnswer(200, `{"a": 1}`),
	}}
	_, problems, _ := second.merge(first.build(), recordedAt)
	want := []string{
		"[operator] the first recording has no answer for this role",
		"[admin] status: 200 in the first recording, 500 in the second",
		`[admin] body a: number 1 in the first recording, string "1" in the second`,
	}
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("problems:\n got %q\nwant %q", problems, want)
	}

	fewer := recording{ID: "GET /api/x", Answers: map[Role]Answer{"admin": jsonAnswer(200, `{"a": 1}`)}}
	_, problems, _ = fewer.merge(first.build(), recordedAt)
	if want := []string{"[viewer] the second recording has no answer for this role"}; !reflect.DeepEqual(problems, want) {
		t.Errorf("a second recording without viewer: problems %q, want %q", problems, want)
	}
}

func TestGoldenFileNames(t *testing.T) {
	got := filepath.ToSlash(goldenFile("testdata/golden", "matrix", "devices", "GET_api_devices__id"))
	if got != "testdata/golden/matrix/devices/GET_api_devices__id.json" {
		t.Errorf("goldenFile = %q", got)
	}
	for in, want := range map[string]string{"a/b": "a-b", `a\b:c*?"<>|`: "a-b-c------", "..": "_", "ok-name_1.x": "ok-name_1.x", "": "_"} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A mark set by hand after a golden was recorded counts at once: the value
// at its path is compared by type only, and a header named by hand only
// has to be present.
func TestHandMarksCountWithoutRecordingAgain(t *testing.T) {
	recorded := Answer{Status: 200, Headers: map[string][]string{"x-rev": {"a1"}}, Body: Body{Kind: BodyJSON, JSON: []byte(`{"n":1,"when":"2026-01-02 03:04:05","list":[1,2]}`)}}
	now := Answer{Status: 200, Headers: map[string][]string{"x-rev": {"b2"}}, Body: Body{Kind: BodyJSON, JSON: []byte(`{"n":7,"when":"1m3.2s","list":[1,2,3]}`)}}
	want := newGoldenAnswer(recorded, []VolatileMark{{Path: "when", Format: "9999-99-99 99:99:99"}}, nil)

	if diffs := CompareAnswers(want, now); len(diffs) != 4 {
		t.Fatalf("without hand marks: %d differences, want 4 (header, n, when, list): %v", len(diffs), diffs)
	}
	marked := withHandMarks(want, []string{"n", "~when", "~list"}, []string{"X-Rev"})
	if diffs := CompareAnswers(marked, now); len(diffs) != 0 {
		t.Errorf("with hand marks: %v", diffs)
	}
	// A hand mark that is not free keeps the recorded format
	kept := withHandMarks(want, []string{"n", "when", "~list"}, []string{"x-rev"})
	if diffs := CompareAnswers(kept, now); len(diffs) != 1 {
		t.Errorf("a plain hand mark must keep the recorded format: %v", diffs)
	}
	// The type still counts
	wrongType := now
	wrongType.Body = Body{Kind: BodyJSON, JSON: []byte(`{"n":"7","when":"x","list":[]}`)}
	if diffs := CompareAnswers(marked, wrongType); len(diffs) != 1 {
		t.Errorf("a volatile value of another type must be reported once: %v", diffs)
	}
	if len(want.Volatile) != 1 || want.Volatile[0].Format == "" {
		t.Error("withHandMarks changed the golden it was given")
	}
}
