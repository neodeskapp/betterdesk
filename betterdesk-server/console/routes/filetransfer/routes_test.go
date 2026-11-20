package filetransfer_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	service "github.com/unitronix/betterdesk-server/console/filetransfer"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/routes/filetransfer"
)

// vector is a request with the Node console's answer, or one of the two
// steps that are no requests (testdata/nodeoracle/run.js).
type vector struct {
	Request *proxytest.Request `json:"request"`
	Status  int                `json:"status"`
	Headers map[string]string  `json:"headers"`
	Body    string             `json:"body"`
	Feed    *struct {
		Transfer string  `json:"transfer"`
		Index    float64 `json:"index"`
		Data     string  `json:"data"`
	} `json:"feed"`
	Remove string `json:"remove"`
}

var (
	transferID = regexp.MustCompile(`[0-9a-fA-F]{32}`)
	recordID   = regexp.MustCompile(`"id":"([0-9a-f]{32})"`)
	tempPath   = regexp.MustCompile(`"tempPath":"[^"]*[/\\]([0-9a-f]{32}_[^"/\\]*)"`)
	stamps     = regexp.MustCompile(`"(createdAt|updatedAt)":[0-9]+`)
)

type world struct {
	*proxytest.World
	transfers *service.Service
	// goID maps the id a transfer had in the Node run to the id it has
	// here, nodeID the other way.
	goID, nodeID map[string]string
}

func newWorld(t *testing.T) *world {
	w := &world{goID: map[string]string{}, nodeID: map[string]string{}}
	// A clock that moves with every reading: no two transfers of a test
	// are made in the same millisecond, so the order of a list is fixed
	clock := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	w.transfers = service.New(service.Options{
		Dir: filepath.Join(t.TempDir(), "data", "transfers"),
		Now: func() time.Time {
			clock = clock.Add(time.Millisecond)
			return clock
		},
	})
	w.World = proxytest.NewWorld(t, func(d *kit.Deps) {
		d.Limits = limits.NewSet(limits.Options{
			ClientIP: httpx.SocketIP,
			UserKey:  func(*http.Request) string { return "" },
			Getenv:   func(string) string { return "100000000" },
		})
		d.FileTransfer = w.transfers
		filetransfer.Register(d.At("/api/files"))
		filetransfer.Register(d.At("/api/bd"))
	})
	return w
}

// local puts the ids of this run in place of the Node run's ids.
func (w *world) local(text string) string {
	return transferID.ReplaceAllStringFunc(text, func(id string) string {
		if mapped, ok := w.goID[strings.ToLower(id)]; ok {
			if id == strings.ToUpper(id) && id != strings.ToLower(id) {
				return strings.ToUpper(mapped)
			}
			return mapped
		}
		return id
	})
}

// normalize makes a JSON body of this run comparable with the Node run's:
// its ids, no times.
func (w *world) normalize(body string) string {
	body = transferID.ReplaceAllStringFunc(body, func(id string) string {
		if mapped, ok := w.nodeID[id]; ok {
			return mapped
		}
		return id
	})
	return stamps.ReplaceAllString(body, `"$1":0`)
}

// TestVectors replays what routes/fileTransfer.routes.js of the Node
// console answered: who may call what, what a transfer is made of, the
// lists, and the download of a completed transfer with ranges and
// conditions. The chunks a device would send are given to the service
// directly, as the Node script did.
func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 500 {
		t.Fatalf("%d vectors, the file should hold more than 500", len(vectors))
	}
	w := newWorld(t)
	etags := map[string]string{}
	failures, unstored, noPath := 0, 0, 0
	for i, v := range vectors {
		switch {
		case v.Feed != nil:
			if _, err := w.transfers.ReceiveChunk(w.goID[v.Feed.Transfer], v.Feed.Index, v.Feed.Data); err != nil {
				t.Fatalf("vector %d: chunk for %s: %v", i, v.Feed.Transfer, err)
			}
			continue
		case v.Remove != "":
			record := w.transfers.GetTransfer(w.goID[v.Remove])
			if record == nil {
				t.Fatalf("vector %d: no transfer %s", i, v.Remove)
			}
			if err := os.Remove(record.TempPath); err != nil {
				t.Fatalf("vector %d: %v", i, err)
			}
			continue
		}
		req := *v.Request
		req.Path = w.local(req.Path)
		if req.Body != nil && req.Type == "" {
			req.Type = "application/json"
		}
		// A condition on the file's tag uses the tag this run gave the file
		if tag, ok := req.Headers["If-None-Match"]; ok && strings.HasPrefix(tag, "W/") {
			headers := map[string]string{}
			for name, value := range req.Headers {
				headers[name] = value
			}
			headers["If-None-Match"] = etags[req.Path]
			req.Headers = headers
		}
		rec, _ := w.Do(req, nil)
		want, _ := base64.StdEncoding.DecodeString(v.Body)
		got := rec.Body.Bytes()
		isJSON := strings.HasPrefix(v.Headers["content-type"], "application/json")

		// A new transfer: remember which id of this run stands for it
		if req.Method == http.MethodPost && strings.HasSuffix(strings.ToLower(req.Path), "/transfer") && v.Status == http.StatusCreated && rec.Code == http.StatusCreated {
			var theirs, ours struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(want, &theirs) == nil && json.Unmarshal(got, &ours) == nil {
				w.goID[theirs.ID], w.nodeID[ours.ID] = ours.ID, theirs.ID
			}
		}
		if rec.Code == http.StatusOK && rec.Header().Get("Content-Disposition") != "" {
			etags[req.Path] = rec.Header().Get("ETag")
		}

		var problems []string
		if rec.Code != v.Status {
			problems = append(problems, "status "+strconv.Itoa(rec.Code)+", want "+strconv.Itoa(v.Status))
		}
		for _, name := range []string{"content-type", "content-disposition", "accept-ranges", "cache-control", "content-range", "x-content-type-options"} {
			if have := rec.Header().Get(name); have != v.Headers[name] {
				problems = append(problems, name+" "+strconv.Quote(have)+", want "+strconv.Quote(v.Headers[name]))
			}
		}
		for _, name := range []string{"etag", "last-modified"} {
			if _, wanted := v.Headers[name]; wanted != (rec.Header().Get(name) != "") {
				problems = append(problems, name+" present: "+strconv.FormatBool(!wanted)+", want "+strconv.FormatBool(wanted))
			}
		}
		if rec.Header().Get("Content-Disposition") != "" && (rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent) {
			if have := rec.Header().Get("Content-Length"); have != v.Headers["content-length"] {
				problems = append(problems, "content-length "+have+", want "+v.Headers["content-length"])
			}
		}
		gotText, wantText := string(got), string(want)
		if isJSON {
			// FT5: a transfer whose device has no text is not stored, where
			// Node listed it although its request failed
			if kept, dropped := withoutUnstored(wantText); dropped {
				wantText = kept
				unstored++
			}
			// A transfer that was made by a request that failed all the same
			// shows up in Node's lists only: pair the unknown ids of the two
			// lists by their places
			ours, theirs := recordID.FindAllStringSubmatch(gotText, -1), recordID.FindAllStringSubmatch(wantText, -1)
			if len(ours) == len(theirs) {
				for n := range ours {
					if w.nodeID[ours[n][1]] == "" && w.goID[theirs[n][1]] == "" {
						w.goID[theirs[n][1]], w.nodeID[ours[n][1]] = ours[n][1], theirs[n][1]
					}
				}
			}
			gotText = w.normalize(gotText)
			// FT6: a record names no path on the server
			if kept, had := withoutTempPath(wantText); had {
				wantText = kept
				noPath++
			}
			wantText = stamps.ReplaceAllString(wantText, `"$1":0`)
		}
		if gotText != wantText {
			problems = append(problems, "body "+around(gotText, wantText)+"\n      want "+around(wantText, gotText))
		}
		if len(problems) > 0 {
			failures++
			if failures <= 15 {
				body := ""
				if req.Body != nil {
					body = " " + *req.Body
				}
				headers, _ := json.Marshal(req.Headers)
				t.Errorf("vector %d: %s %s%s %s:\n  %s", i, req.Method, v.Request.Path, body, headers, strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 15 {
		t.Errorf("%d of %d vectors differ; the first 15 are shown", failures, len(vectors))
	}
	if unstored == 0 {
		t.Error("no list holds a transfer without a text: the fix of FT5 changes no vector")
	}
	if noPath == 0 {
		t.Error("no answer names the path of a file: the fix of FT6 changes no vector")
	}
}

// nodeTempPath is the path of a transfer's file in a record of the Node
// run.
var nodeTempPath = regexp.MustCompile(`,"tempPath":"(?:[^"\\]|\\.)*"`)

// withoutTempPath is a JSON answer of the Node run without the paths of
// the files, and whether it named one.
func withoutTempPath(text string) (string, bool) {
	kept := nodeTempPath.ReplaceAllString(text, "")
	return kept, kept != text
}

// withoutUnstored is a list of transfers of the Node run without the
// ones whose device or direction has no text (an object with a member
// named toString), and whether it had any.
func withoutUnstored(list string) (string, bool) {
	var records []json.RawMessage
	if !strings.HasPrefix(list, "[") || json.Unmarshal([]byte(list), &records) != nil {
		return list, false
	}
	kept := make([]string, 0, len(records))
	for _, record := range records {
		var fields struct {
			DeviceID  json.RawMessage `json:"deviceId"`
			Direction json.RawMessage `json:"direction"`
		}
		_ = json.Unmarshal(record, &fields)
		if noText(fields.DeviceID) || noText(fields.Direction) {
			continue
		}
		kept = append(kept, string(record))
	}
	if len(kept) == len(records) {
		return list, false
	}
	return "[" + strings.Join(kept, ",") + "]", true
}

// noText reports whether a JSON value is an object with a member named
// toString.
func noText(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) != nil {
		return false
	}
	_, has := object["toString"]
	return has
}

// around is the part of text where it starts to differ from other, with
// some of what comes before and after.
func around(text, other string) string {
	at := 0
	for at < len(text) && at < len(other) && text[at] == other[at] {
		at++
	}
	from, to := max(at-120, 0), min(at+200, len(text))
	out := strconv.Quote(text[from:to])
	if from > 0 {
		out = "..." + out
	}
	if to < len(text) {
		out += "..."
	}
	return out
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true), Limits: limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }})}
	filetransfer.Register(d.At("/api/files"))
	filetransfer.Register(d.At("/api/bd"))
	var got, want []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	// In the order of the Node file, once per mount
	for _, mount := range []string{"/api/files", "/api/bd"} {
		want = append(want,
			"POST "+mount+"/transfer",
			"GET "+mount+"/transfers",
			"GET "+mount+"/transfers/{id}",
			"POST "+mount+"/transfers/{id}/cancel",
			"GET "+mount+"/transfers/{id}/download",
			"POST "+mount+"/file-transfer",
			"GET "+mount+"/file-transfer/{id}",
		)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
