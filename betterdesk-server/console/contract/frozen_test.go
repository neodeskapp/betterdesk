package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// movedConsole is a console where some routes have moved to Go: a fake
// console with the routing report, POST /api/make (answers a new ID each
// time) and GET /api/item/<id>.
type movedConsole struct {
	// handler is what the routing report says for a method and path. nil
	// is a console without the report.
	handler func(method, path string) string
	made    int
	seen    []string
}

// newMovedSuite starts a movedConsole and returns a suite that talks to it
// with the given record mode and golden folder.
func newMovedSuite(t *testing.T, mode RecordMode, goldenDir string, handler func(method, path string) string) (*Suite, *movedConsole) {
	t.Helper()
	console := &movedConsole{handler: handler}
	fake := &fakeConsole{t: t, password: "the-password"}
	plumbing := fake.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer := func(v any) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.URL.Path == "/__bd/routing":
			if console.handler == nil {
				http.NotFound(w, r)
				return
			}
			answer(map[string]string{"handler": console.handler(r.URL.Query().Get("method"), r.URL.Query().Get("path"))})
		case r.Method == "POST" && r.URL.Path == "/api/make":
			console.made++
			console.seen = append(console.seen, "POST /api/make")
			answer(map[string]any{"data": map[string]any{"id": fmt.Sprintf("item-%d", console.made)}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/item/"):
			console.seen = append(console.seen, "GET "+r.URL.Path)
			answer(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/item/"), "stable": true})
		default:
			plumbing.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	passwordFile := filepath.Join(t.TempDir(), "e2e_password")
	if err := os.WriteFile(passwordFile, []byte("the-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(srv.URL)
	settings := &Settings{BaseURL: base, PasswordFile: passwordFile, Timeout: 2 * time.Second, Record: mode, GoldenDir: goldenDir}
	client, err := NewClient(settings)
	if err != nil {
		t.Fatal(err)
	}
	return &Suite{
		Settings: settings, Client: client, Report: newReport(settings), Now: time.Now(),
		sessions: map[Role]*Session{}, goldens: map[string]*Golden{},
	}, console
}

// makeMovedToGo is a routing report where POST /api/make is answered by Go
// and everything else by Node.
func makeMovedToGo(method, path string) string {
	if method == "POST" && path == "/api/make" {
		return HandlerGo
	}
	return HandlerNode
}

// nodeGolden writes a golden as the Node console's recording left it and
// returns its bytes.
func nodeGolden(t *testing.T, file, id, method, path string) []byte {
	t.Helper()
	golden := &Golden{
		ID: id, Request: RequestInfo{Method: method, Path: path}, Recordings: 2,
		Answers: []GoldenAnswer{{
			Roles: []Role{"admin"}, Status: 200,
			Body: Body{Kind: BodyJSON, JSON: json.RawMessage(`{"recorded":"from Node, before the route moved"}`)},
		}},
	}
	if err := golden.Save(file); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func movedGroup() *CaseGroup {
	var group *CaseGroup
	withGroups(func() {
		Group("moved",
			Case{Name: "make", Method: "POST", Path: "/api/make", Roles: Roles("admin"), Capture: map[string]string{"item": "data.id"}},
			Case{Name: "read", Path: "/api/item/{item}", Roles: Roles("admin")},
		)
		group = Groups()[0]
	})
	return group
}

// A recording sends the request of a route the Go console answers, because
// the next step needs what it made, but leaves its golden as the Node
// console recorded it. The step the Node console answers is recorded.
func TestRecordingLeavesTheGoldenOfAGoRouteAsItIs(t *testing.T) {
	dir := t.TempDir()
	makeFile := goldenFile(dir, "cases", "moved", "make")
	readFile := goldenFile(dir, "cases", "moved", "read")
	before := nodeGolden(t, makeFile, "moved/make", "POST", "/api/make")

	for _, mode := range []RecordMode{RecordFirst, RecordMerge} {
		s, console := newMovedSuite(t, mode, dir, makeMovedToGo)
		s.runGroup(t, movedGroup())

		if got := strings.Join(console.seen, ", "); got != "POST /api/make, GET /api/item/item-1" {
			t.Fatalf("mode %d: the console got %q: the step answered by Go must run, and the next one with its ID", mode, got)
		}
		after, err := os.ReadFile(makeFile)
		if err != nil || string(after) != string(before) {
			t.Fatalf("mode %d: the golden of the route Go answers was rewritten (%v):\n%s", mode, err, after)
		}
		read, err := LoadGolden(readFile)
		if err != nil {
			t.Fatalf("mode %d: the step Node answers has no golden: %v", mode, err)
		}
		if want := int(mode); read.Recordings != want {
			t.Errorf("mode %d: the golden of the Node step has %d recordings, want %d", mode, read.Recordings, want)
		}
		var recorded struct{ ID string }
		answer, _ := read.For("admin")
		if err := json.Unmarshal(answer.Body.JSON, &recorded); err != nil || recorded.ID != "item-1" {
			t.Errorf("mode %d: the Node step was not recorded with the captured ID: %s (%v)", mode, answer.Body.JSON, err)
		}
		if len(s.Report.frozen) != 1 || !s.Report.frozen[makeFile] || s.Report.files != 1 {
			t.Errorf("mode %d: frozen %v, files written %d; want the one frozen golden and one written", mode, s.Report.frozen, s.Report.files)
		}
		var summary strings.Builder
		s.Report.Print(&summary)
		if !strings.Contains(summary.String(), "frozen: 1 ") {
			t.Errorf("mode %d: the summary does not say \"frozen: 1\":\n%s", mode, summary.String())
		}
	}
}

// A route the Go console answers that has no golden yet gets none from a
// recording: Go's own answer is never the specification.
func TestRecordingWritesNoGoldenFromAGoAnswer(t *testing.T) {
	dir := t.TempDir()
	s, _ := newMovedSuite(t, RecordFirst, dir, makeMovedToGo)
	s.runGroup(t, movedGroup())
	if _, err := os.Stat(goldenFile(dir, "cases", "moved", "make")); !isNotExist(err) {
		t.Errorf("a golden was written from the Go console's answer (stat: %v)", err)
	}
}

// CONTRACT_RECORD_NEW=1 records a case the Go console answers when its
// golden is new: none yet in the first recording, the first recording's
// in the second. A golden that holds both recordings is never written, and
// without the setting a Go answer is not recorded at all.
func TestRecordNewWritesOnlyNewGoldens(t *testing.T) {
	dir := t.TempDir()
	makeFile := goldenFile(dir, "cases", "moved", "make")
	for _, mode := range []RecordMode{RecordFirst, RecordMerge} {
		s, _ := newMovedSuite(t, mode, dir, makeMovedToGo)
		s.Settings.RecordNew = true
		s.runGroup(t, movedGroup())
		golden, err := LoadGolden(makeFile)
		if err != nil {
			t.Fatalf("mode %d: the new golden of the route Go answers was not written: %v", mode, err)
		}
		if golden.Recordings != int(mode) || !s.Report.recordedNew[makeFile] {
			t.Errorf("mode %d: %d recordings, counted as new %v", mode, golden.Recordings, s.Report.recordedNew[makeFile])
		}
		var summary strings.Builder
		s.Report.Print(&summary)
		if !strings.Contains(summary.String(), "recorded new: 1 ") {
			t.Errorf("mode %d: the summary does not count the new golden:\n%s", mode, summary.String())
		}
	}
	merged, _ := os.ReadFile(makeFile)

	// Once both recordings are in, the golden is frozen like any other
	for _, mode := range []RecordMode{RecordFirst, RecordMerge} {
		s, _ := newMovedSuite(t, mode, dir, makeMovedToGo)
		s.Settings.RecordNew = true
		s.runGroup(t, movedGroup())
		if after, _ := os.ReadFile(makeFile); string(after) != string(merged) {
			t.Fatalf("mode %d: a golden with two recordings was written again:\n%s", mode, after)
		}
		if len(s.Report.recordedNew) != 0 || !s.Report.frozen[makeFile] {
			t.Errorf("mode %d: recorded new %v, frozen %v; want the golden frozen", mode, s.Report.recordedNew, s.Report.frozen)
		}
	}

	// A Node golden that exists is recorded as always, and a Go route
	// without the setting gets nothing
	other := t.TempDir()
	s, _ := newMovedSuite(t, RecordFirst, other, makeMovedToGo)
	s.runGroup(t, movedGroup())
	if _, err := os.Stat(goldenFile(other, "cases", "moved", "make")); !isNotExist(err) {
		t.Errorf("without CONTRACT_RECORD_NEW a golden was written from a Go answer (stat: %v)", err)
	}
}

func TestRecordableByHandler(t *testing.T) {
	for handler, want := range map[string]struct{ record, problem bool }{
		HandlerNode:    {true, false},
		HandlerGo:      {false, false},
		HandlerNone:    {false, true},
		HandlerUnknown: {false, true},
	} {
		record, problem := recordable(handler)
		if record != want.record || (problem != "") != want.problem {
			t.Errorf("recordable(%q) = %v, %q; want record %v, a problem %v", handler, record, problem, want.record, want.problem)
		}
	}
}

// Without the routing report nothing says which goldens are frozen, so a
// recording must not start. Comparing works without it.
func TestRecordingNeedsTheRoutingReport(t *testing.T) {
	for _, c := range []struct {
		mode    RecordMode
		routing bool
		blocked bool
	}{
		{RecordFirst, false, true},
		{RecordMerge, false, true},
		{Compare, false, false},
		{RecordFirst, true, false},
		{RecordMerge, true, false},
	} {
		var handler func(method, path string) string
		if c.routing {
			handler = makeMovedToGo
		}
		s, _ := newMovedSuite(t, c.mode, t.TempDir(), handler)
		if problem := s.recordingBlocked(); (problem != "") != c.blocked {
			t.Errorf("mode %d, routing report %v: recordingBlocked() = %q, want blocked %v", c.mode, c.routing, problem, c.blocked)
		}
	}
}

// A first recording removes the goldens nothing stands for any more, but
// not one whose request the Go console answers, and nothing in other modes.
func TestPruneKeepsFrozenGoldens(t *testing.T) {
	for _, mode := range []RecordMode{RecordFirst, RecordMerge, Compare} {
		dir := t.TempDir()
		layer := filepath.Join(dir, "cases")
		current := goldenFile(dir, "cases", "moved", "read")
		staleNode := goldenFile(dir, "cases", "gone", "list")
		staleGo := goldenFile(dir, "cases", "old", "make")
		staleGoQuery := goldenFile(dir, "cases", "moved", "make-with-a-query")
		nodeGolden(t, current, "moved/read", "GET", "/api/item/{item}")
		nodeGolden(t, staleNode, "gone/list", "GET", "/api/item/{item}?x=1")
		nodeGolden(t, staleGo, "old/make", "POST", "/api/make")
		nodeGolden(t, staleGoQuery, "moved/make-with-a-query", "POST", "/api/make?dry=1")

		s, _ := newMovedSuite(t, mode, dir, makeMovedToGo)
		s.prune(t, layer, map[string]bool{filepath.Clean(current): true})

		exists := func(file string) bool {
			_, err := os.Stat(file)
			return err == nil
		}
		wantGone := mode == RecordFirst
		if !exists(current) || !exists(staleGo) || !exists(staleGoQuery) {
			t.Errorf("mode %d: a golden that is in use or frozen was removed", mode)
		}
		if exists(staleNode) == wantGone || exists(filepath.Dir(staleNode)) == wantGone {
			t.Errorf("mode %d: the golden nothing stands for exists = %v, its folder = %v; want gone = %v",
				mode, exists(staleNode), exists(filepath.Dir(staleNode)), wantGone)
		}
		if wantGone && (s.Report.removed != 1 || len(s.Report.frozen) != 2) {
			t.Errorf("mode %d: removed %d, frozen %d; want 1 and 2", mode, s.Report.removed, len(s.Report.frozen))
		}
	}
}
