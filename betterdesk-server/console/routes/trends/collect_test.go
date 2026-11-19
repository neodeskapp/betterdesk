package trends

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// scenario is one entry of testdata/trends.json: answers of the API client
// and what the Node route made of them (testdata/nodeoracle/trends.js).
type scenario struct {
	Name     string                     `json:"name"`
	Now      int64                      `json:"now"`
	Query    string                     `json:"query"`
	Peers    json.RawMessage            `json:"peers"`
	Sessions map[string]json.RawMessage `json:"sessions"`
	Help     json.RawMessage            `json:"help"`
	Dates    []string                   `json:"dates"`
	Answer   struct {
		Status int `json:"status"`
		Body   struct {
			Success     bool            `json:"success"`
			Data        json.RawMessage `json:"data"`
			GeneratedAt int64           `json:"generatedAt"`
			Error       string          `json:"error"`
		} `json:"body"`
	} `json:"answer"`
	Calls []json.RawMessage `json:"calls"`
}

func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	raw, err := os.ReadFile("testdata/trends.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []scenario
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) < 50 {
		t.Fatalf("testdata/trends.json holds %d scenarios", len(out))
	}
	return out
}

// fakeSource answers like the API client did in a scenario.
type fakeSource struct {
	t  *testing.T
	sc scenario

	mu    sync.Mutex
	calls []string
}

func (f *fakeSource) note(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeSource) parse(raw json.RawMessage) any {
	v, err := goapi.Parse(raw)
	if err != nil {
		f.t.Fatalf("scenario %q: %v", f.sc.Name, err)
	}
	return v
}

func (f *fakeSource) GetAllPeers(_ context.Context, includeDeleted bool) []*goapi.Object {
	if includeDeleted {
		f.t.Errorf("scenario %q: deleted devices were asked for", f.sc.Name)
	}
	f.note("getAllPeers")
	list, _ := f.parse(f.sc.Peers).([]any)
	var peers []*goapi.Object
	for _, p := range list {
		o, _ := p.(*goapi.Object)
		peers = append(peers, o)
	}
	return peers
}

func (f *fakeSource) GetPeerConnectionSessions(_ context.Context, id string, limit any) *goapi.Object {
	f.note("getPeerConnectionSessions " + id + " " + jsreq.String(limit))
	raw, ok := f.sc.Sessions[id]
	if !ok {
		return goapi.NewObject("success", true, "data", goapi.NewObject("peer_id", id, "count", float64(0), "sessions", []any{}))
	}
	o, _ := f.parse(raw).(*goapi.Object)
	return o
}

func (f *fakeSource) ListHelpRequests(_ context.Context, filter any) *goapi.Object {
	limit, _ := filter.(*goapi.Object)
	f.note("listHelpRequests " + jsreq.String(limit.Get("limit")))
	if f.sc.Help == nil {
		return goapi.NewObject("success", true, "data", []any{})
	}
	o, _ := f.parse(f.sc.Help).(*goapi.Object)
	return o
}

// wantCalls turns the calls the Node route made into the notes of
// fakeSource.
func wantCalls(t *testing.T, sc scenario) []string {
	t.Helper()
	var out []string
	for _, raw := range sc.Calls {
		var call []any
		if err := json.Unmarshal(raw, &call); err != nil {
			t.Fatal(err)
		}
		switch call[0] {
		case "getAllPeers":
			if options := call[1].(map[string]any); options["includeDeleted"] != false {
				t.Fatalf("scenario %q: Node asked for the devices with %v", sc.Name, options)
			}
			out = append(out, "getAllPeers")
		case "getPeerConnectionSessions":
			id, err := goapi.FromGo(call[1])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, "getPeerConnectionSessions "+jsreq.String(id)+" "+jsreq.String(call[2].(map[string]any)["limit"]))
		case "listHelpRequests":
			out = append(out, "listHelpRequests "+jsreq.String(call[1].(map[string]any)["limit"]))
		}
	}
	return out
}

func TestCollectMatchesNode(t *testing.T) {
	for _, sc := range loadScenarios(t) {
		src := &fakeSource{t: t, sc: sc}
		days := daysOf(jsreq.QueryValue(sc.Query, "days"))
		got, err := collect(context.Background(), src, days, sc.Now)

		// Node asked for the sessions in the order of the devices; here
		// six workers share them, so the order between them is free
		want := wantCalls(t, sc)
		sort.Strings(want)
		sort.Strings(src.calls)
		if len(want) != len(src.calls) {
			t.Errorf("%s: %d calls, Node made %d", sc.Name, len(src.calls), len(want))
		} else {
			for i := range want {
				if want[i] != src.calls[i] {
					t.Errorf("%s: call %q, Node made %q", sc.Name, src.calls[i], want[i])
					break
				}
			}
		}

		if sc.Answer.Status != 200 {
			if err == nil {
				t.Errorf("%s: collected %+v, Node answered %d", sc.Name, got, sc.Answer.Status)
			}
			if sc.Answer.Status != 502 || sc.Answer.Body.Error != "T(errors.server_error)" {
				t.Errorf("%s: Node answered %d %q", sc.Name, sc.Answer.Status, sc.Answer.Body.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v, Node answered %s", sc.Name, err, sc.Answer.Body.Data)
			continue
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != string(sc.Answer.Body.Data) {
			t.Errorf("%s:\n got %s\nNode %s", sc.Name, encoded, sc.Answer.Body.Data)
		}
		if sc.Answer.Body.GeneratedAt != sc.Now {
			t.Errorf("%s: Node's generatedAt is %d, the clock said %d", sc.Name, sc.Answer.Body.GeneratedAt, sc.Now)
		}
	}
}

// TestParseDateMatchesNode replays the scenario that sends every kind of
// time text through the Node route, one text at a time.
func TestParseDateMatchesNode(t *testing.T) {
	var sc scenario
	for _, candidate := range loadScenarios(t) {
		if candidate.Name == "times as text" {
			sc = candidate
		}
	}
	if len(sc.Dates) < 50 {
		t.Fatalf("the scenario with times holds %d of them", len(sc.Dates))
	}
	var data struct {
		Help []struct {
			Created *int64 `json:"created"`
			Status  string `json:"status"`
		} `json:"help"`
	}
	if err := json.Unmarshal(sc.Answer.Body.Data, &data); err != nil {
		t.Fatal(err)
	}
	node := map[string]int64{}
	for _, h := range data.Help {
		if h.Created == nil {
			t.Fatalf("Node kept the time %s without a value", h.Status)
		}
		node[h.Status] = *h.Created
	}
	read := 0
	for i, text := range sc.Dates {
		want, ok := node[jsreq.String(float64(i))]
		got, gotOK := toTime(text)
		if ok != gotOK || got != want {
			t.Errorf("toTime(%q) = %d, %v; Node gave %d, %v", text, got, gotOK, want, ok)
		}
		if ok {
			read++
		}
	}
	if read < 20 {
		t.Errorf("Node read only %d of the times", read)
	}
}

func TestToTimeOfOtherValues(t *testing.T) {
	for _, v := range []any{nil, "", false, float64(0), []any{}, goapi.NewObject(), true, float64(1731224000000), []any{"x"}} {
		if ms, ok := toTime(v); ok {
			t.Errorf("toTime(%v) = %d, want no time", v, ms)
		}
	}
	// A list of one text reads as that text
	if ms, ok := toTime([]any{"2026-11-10 08:00:00"}); !ok || ms != time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("toTime of a list of one = %d, %v", ms, ok)
	}
}

func TestDaysOf(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int
	}{
		{nil, 14}, {"", 14}, {"abc", 14}, {"0", 14}, {"30", 30}, {"-3", 1}, {"365", 90}, {"90", 90}, {"91", 90},
		{"1", 1}, {"7d", 7}, {[]any{"7", "30"}, 7}, {goapi.NewObject("x", "5"), 14}, {"2.9", 2}, {"1e3", 1},
	} {
		if got := daysOf(tc.v); got != tc.want {
			t.Errorf("daysOf(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

// blockingSource is an API client whose device list waits until it is
// released, and that counts how often the list was asked for.
type blockingSource struct {
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	peers int
}

func (b *blockingSource) GetAllPeers(context.Context, bool) []*goapi.Object {
	b.mu.Lock()
	b.peers++
	b.mu.Unlock()
	b.entered <- struct{}{}
	<-b.release
	return []*goapi.Object{goapi.NewObject("id", "a")}
}

// GetPeerConnectionSessions answers one session, and fails as the API
// client does once the context has ended.
func (b *blockingSource) GetPeerConnectionSessions(ctx context.Context, _ string, _ any) *goapi.Object {
	if ctx.Err() != nil {
		return goapi.NewObject("success", false, "error", ctx.Err().Error())
	}
	session := goapi.NewObject("id", float64(1), "started_at", time.Now().UTC().Format(time.RFC3339))
	return goapi.NewObject("success", true, "data", goapi.NewObject("sessions", []any{session}))
}

func (b *blockingSource) ListHelpRequests(context.Context, any) *goapi.Object {
	return goapi.NewObject("success", true, "data", []any{})
}

// TestRequestsDuringACollectionShareIt sends a second request while the
// first one's collection waits for the device list: it waits for that
// collection instead of starting its own, and both get its result.
func TestRequestsDuringACollectionShareIt(t *testing.T) {
	src := &blockingSource{entered: make(chan struct{}, 2), release: make(chan struct{})}
	h := &handlers{api: src, now: time.Now}
	type outcome struct {
		entry *cached
		err   error
	}
	results := make(chan outcome, 2)
	ask := func() {
		entry, err := h.collected(context.Background(), 14)
		results <- outcome{entry, err}
	}
	go ask()
	<-src.entered
	go ask()
	select {
	case <-src.entered:
		t.Error("the second request started a collection of its own")
	case <-time.After(500 * time.Millisecond):
	}
	close(src.release)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.entry == nil || first.entry != second.entry {
		t.Errorf("the two requests got %+v and %+v", first, second)
	}
	if src.peers != 1 {
		t.Errorf("the device list was asked for %d times", src.peers)
	}
	// A request after the collection ended reads what was kept
	if entry, err := h.collected(context.Background(), 14); err != nil || entry != first.entry {
		t.Errorf("a later request got %+v, %v", entry, err)
	}
}

// TestACollectionOutlivesTheRequestThatStartedIt: the request that
// started a collection goes away, the collection goes on, and a request
// that waited for it gets its result.
func TestACollectionOutlivesTheRequestThatStartedIt(t *testing.T) {
	src := &blockingSource{entered: make(chan struct{}, 2), release: make(chan struct{})}
	h := &handlers{api: src, now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error, 1)
	go func() {
		_, err := h.collected(ctx, 14)
		waited <- err
	}()
	<-src.entered
	second := make(chan *cached, 1)
	go func() {
		entry, _ := h.collected(context.Background(), 14)
		second <- entry
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	close(src.release)
	if err := <-waited; err != nil && err != context.Canceled {
		t.Errorf("the first request ended with %v", err)
	}
	if entry := <-second; entry == nil || len(entry.data.Sessions) != 1 {
		t.Errorf("the request that waited got %+v", entry)
	}
}
