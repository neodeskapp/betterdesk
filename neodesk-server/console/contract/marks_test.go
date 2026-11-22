package contract

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// An array marked AnyOrder passes with its elements in another order, and
// fails with an element missing, one too many or one changed.
func TestCompareArrayInAnyOrder(t *testing.T) {
	want := golden(jsonAnswer(200, `{"data": {"permissions": ["a.view", "b.edit", "c.delete"]}}`), AnyOrder+"data.permissions")
	if got := want.Volatile; len(got) != 1 || got[0].Path != "data.permissions" || !got[0].AnyOrder {
		t.Fatalf("marks = %+v, want data.permissions in any order", got)
	}
	cases := []struct {
		name, body string
		diffs      []string
	}{
		{"another order", `{"data": {"permissions": ["c.delete", "a.view", "b.edit"]}}`, nil},
		{"one missing", `{"data": {"permissions": ["c.delete", "a.view"]}}`,
			[]string{`body data.permissions: missing element (in any order) "b.edit"`}},
		{"one too many", `{"data": {"permissions": ["c.delete", "a.view", "b.edit", "user.delete"]}}`,
			[]string{`body data.permissions: unexpected element (in any order) "user.delete"`}},
		{"one twice", `{"data": {"permissions": ["a.view", "a.view", "b.edit"]}}`,
			[]string{`body data.permissions: missing element (in any order) "c.delete"`, `body data.permissions: unexpected element (in any order) "a.view"`}},
		{"not a list", `{"data": {"permissions": "a.view"}}`,
			[]string{`body data.permissions: type: expected array ["a.view","b.edit","c.delete"], got string "a.view"`}},
	}
	for _, c := range cases {
		diffs := diffStrings(CompareAnswers(want, jsonAnswer(200, c.body)))
		if len(diffs) == 0 {
			diffs = nil
		}
		if !reflect.DeepEqual(diffs, c.diffs) {
			t.Errorf("%s: diffs\n got %q\nwant %q", c.name, diffs, c.diffs)
		}
	}

	// [*] marks the list of every element
	roles := golden(jsonAnswer(200, `{"roles": [{"name": "viewer", "permissions": ["a", "b"]}, {"name": "admin", "permissions": ["c"]}]}`), AnyOrder+"roles[*].permissions")
	if diffs := CompareAnswers(roles, jsonAnswer(200, `{"roles": [{"name": "viewer", "permissions": ["b", "a"]}, {"name": "admin", "permissions": ["c"]}]}`)); len(diffs) > 0 {
		t.Errorf("roles in another order differ: %q", diffStrings(diffs))
	}
	if diffs := CompareAnswers(roles, jsonAnswer(200, `{"roles": [{"name": "viewer", "permissions": ["b", "a"]}, {"name": "admin", "permissions": ["c", "a"]}]}`)); len(diffs) != 1 {
		t.Errorf("an extra permission of admin gives %q, want one difference", diffStrings(diffs))
	}
}

// A recording keeps an array in any order sorted, and two recordings with
// other elements are a problem, not a volatile value.
func TestMergeArrayInAnyOrder(t *testing.T) {
	marks := []string{AnyOrder + "list"}
	first := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200, `{"list": ["b", "c", "a"], "n": 1}`)}}
	second := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200, `{"list": ["c", "a", "b"], "n": 2}`)}}
	golden, problems, _ := second.merge(first.build(), recordedAt)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	answer := golden.Answers[0]
	if got := string(answer.Body.JSON); got != `{"list":["a","b","c"],"n":0}` {
		t.Errorf("body = %s, want the list sorted and n masked", got)
	}
	data, err := marshalPlain(answer.Volatile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `[{"path":"list","anyOrder":true},{"path":"n","integer":true}]` {
		t.Errorf("marks = %s", got)
	}
	if diffs := CompareAnswers(answer, jsonAnswer(200, `{"list": ["c", "b", "a"], "n": 7}`)); len(diffs) > 0 {
		t.Errorf("the merged golden refuses its own answer: %q", diffStrings(diffs))
	}

	// An empty list stays an empty list, not null
	empty := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200, `{"list": [], "n": 1}`)}}
	emptyGolden, problems, _ := empty.merge(empty.build(), recordedAt)
	if len(problems) > 0 || string(emptyGolden.Answers[0].Body.JSON) != `{"list":[],"n":1}` {
		t.Errorf("empty list: %s, problems %q", emptyGolden.Answers[0].Body.JSON, problems)
	}

	other := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200, `{"list": ["a", "b", "d"], "n": 2}`)}}
	_, problems, _ = other.merge(first.build(), recordedAt)
	if len(problems) != 1 || !strings.Contains(problems[0], `only in the first recording: (in any order) "c"`) || !strings.Contains(problems[0], `only in the second recording: (in any order) "d"`) {
		t.Errorf("problems = %q, want the elements that differ", problems)
	}
}

// Two recordings of a list whose length differs: the golden keeps the
// first element as the sample of all, with what differs between the
// elements marked below the array, and every element of a later answer
// must be like the sample. A golden recorded before keeps the type check.
func TestLengthFreeArrayKeepsASampleElement(t *testing.T) {
	marks := []string{"users[#]"}
	first := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200,
		`{"users": [{"id": 1, "username": "ann", "role": "viewer", "last_login": "2026-11-01 01:02:03", "groups": [], "note": null}]}`)}}
	second := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200,
		`{"users": [{"id": 1, "username": "ann", "role": "viewer", "last_login": "2026-11-02 01:02:03", "groups": [], "note": null},`+
			`{"id": 2, "username": "bobby", "role": "viewer", "last_login": "2026-11-03 04:05:06", "groups": [], "note": null}]}`)}}
	merged, problems, _ := second.merge(first.build(), recordedAt)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	want := merged.Answers[0]
	if got := string(want.Body.JSON); got != `{"users":[{"groups":[],"id":0,"last_login":"<volatile>","note":null,"role":"viewer","username":"<volatile>"}]}` {
		t.Errorf("sample = %s", got)
	}
	data, _ := marshalPlain(want.Volatile)
	if got := string(data); got != `[{"path":"users[#]","elements":true},{"path":"users[*].id","integer":true},{"path":"users[*].last_login","format":"9999-99-99 99:99:99"},{"path":"users[*].username","pattern":"^a+$"}]` {
		t.Errorf("marks = %s", got)
	}

	cases := []struct {
		name, body string
		diffs      []string
	}{
		{"more users", `{"users": [{"id": 1, "username": "ann", "role": "viewer", "last_login": "2026-11-10 01:02:03", "groups": [], "note": null}, {"id": 9, "username": "eve", "role": "viewer", "last_login": "2026-11-11 04:05:06", "groups": [], "note": null}, {"id": 3, "username": "jo", "role": "viewer", "last_login": "2026-11-12 04:05:06", "groups": [], "note": null}]}`, nil},
		{"none", `{"users": []}`, nil},
		{"a field that must not go out", `{"users": [{"id": 1, "username": "ann", "role": "viewer", "last_login": "2026-11-10 01:02:03", "groups": [], "note": null, "password_hash": "x"}]}`,
			[]string{`body users[0]: unexpected key "password_hash" with value "x"`}},
		{"a missing field", `{"users": [{"id": 1, "role": "viewer", "last_login": "2026-11-10 01:02:03", "groups": [], "note": null}]}`,
			[]string{`body users[0]: missing key "username"`}},
		{"a value all elements share", `{"users": [{"id": 1, "username": "ann", "role": "admin", "last_login": "2026-11-10 01:02:03", "groups": [], "note": null}]}`,
			[]string{`body users[0].role: expected "viewer", got "admin"`}},
		{"another type", `{"users": [{"id": "1", "username": "ann", "role": "viewer", "last_login": "2026-11-10 01:02:03", "groups": [], "note": null}]}`,
			[]string{`body users[0].id: type: expected number 0, got string "1"`}},
		{"another format", `{"users": [{"id": 1, "username": "ann", "role": "viewer", "last_login": "2026-11-10T01:02:03Z", "groups": [], "note": null}]}`,
			[]string{`body users[0].last_login: format of a volatile value: expected format 9999-99-99 99:99:99, got "2026-11-10T01:02:03Z" (9999-99-99A99:99:99A)`}},
	}
	for _, c := range cases {
		diffs := diffStrings(CompareAnswers(want, jsonAnswer(200, c.body)))
		if len(diffs) == 0 {
			diffs = nil
		}
		if !reflect.DeepEqual(diffs, c.diffs) {
			t.Errorf("%s: diffs\n got %q\nwant %q", c.name, diffs, c.diffs)
		}
	}

	// A golden recorded before samples were kept checks the type only.
	old := golden(jsonAnswer(200, `{"users": [{}]}`), "users[#]")
	if diffs := CompareAnswers(old, jsonAnswer(200, `{"users": [{"id": 1, "password_hash": "x"}]}`)); len(diffs) > 0 {
		t.Errorf("an old emptied sample: %q", diffStrings(diffs))
	}
}

// Elements whose keys or types are not those of the first element are a
// problem: no sample could stand for them.
func TestDetectVolatileReportsElementsUnlikeTheFirst(t *testing.T) {
	first := jsonAnswer(200, `{"list": [{"a": 1}, {"a": 2}]}`)
	second := jsonAnswer(200, `{"list": [{"a": 1}, {"a": 2}, {"b": 3}]}`)
	found := detectVolatile(first, second, nil, nil, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC))
	if !reflect.DeepEqual(found.Volatile, []string{"list[#]", "list[*].a"}) {
		t.Errorf("volatile = %q, want the length and a", found.Volatile)
	}
	if len(found.Problems) != 1 || !strings.Contains(found.Problems[0], `body list[2]: an element is not like the first element`) || !strings.Contains(found.Problems[0], `keys differ`) {
		t.Errorf("problems = %q, want the third element of the second recording", found.Problems)
	}
}

// A key marked Optional may be missing on either side, in a recording and
// in a check; where it is there, its value is volatile. Other keys still
// have to be there.
func TestOptionalKey(t *testing.T) {
	marks := []string{"events[#]", Optional + "events[*].details"}
	first := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200,
		`{"events": [{"action": "a", "details": {"x": 1}}, {"action": "a"}]}`)}}
	second := &recording{ID: "x", Volatile: marks, Answers: map[Role]Answer{"admin": jsonAnswer(200,
		`{"events": [{"action": "a"}, {"action": "a", "details": {"y": "z"}}, {"action": "a"}]}`)}}
	merged, problems, _ := second.merge(first.build(), recordedAt)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	want := merged.Answers[0]
	if got := string(want.Body.JSON); got != `{"events":[{"action":"a","details":{}}]}` {
		t.Errorf("body = %s", got)
	}
	passes := []string{`{"events": [{"action": "a"}, {"action": "a", "details": {"q": [1]}}]}`, `{"events": []}`}
	for _, body := range passes {
		if diffs := CompareAnswers(want, jsonAnswer(200, body)); len(diffs) > 0 {
			t.Errorf("%s: %q", body, diffStrings(diffs))
		}
	}
	diffs := diffStrings(CompareAnswers(want, jsonAnswer(200, `{"events": [{"details": {}}, {"action": "b"}, {"action": "a", "details": "text"}]}`)))
	wantDiffs := []string{
		`body events[0]: missing key "action"`,
		`body events[1].action: expected "a", got "b"`,
		`body events[2].details: type: expected object {}, got string "text"`,
	}
	if !reflect.DeepEqual(diffs, wantDiffs) {
		t.Errorf("diffs\n got %q\nwant %q", diffs, wantDiffs)
	}

	// Without the mark the key is not optional.
	plainFirst := &recording{ID: "x", Volatile: []string{"events[#]"}, Answers: first.Answers}
	plain := &recording{ID: "x", Volatile: []string{"events[#]"}, Answers: second.Answers}
	if _, problems, _ := plain.merge(plainFirst.build(), recordedAt); len(problems) == 0 {
		t.Error("elements with and without details passed without the mark")
	}
}
