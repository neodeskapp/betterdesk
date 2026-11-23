package filetransfer_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// deviceVector is one request of testdata/device_vectors.json with the
// Node console's answer (testdata/README.md).
type deviceVector struct {
	Request struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Body    *string           `json:"body"`
		Type    string            `json:"type"`
		Role    string            `json:"role"`
	} `json:"request"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// deviceTokens are the token rows testdata/device_vectors.json was
// recorded with: token, client id, expiry, revoked. They belong to the
// operator account (id 5) of the test world.
var deviceTokens = [][4]any{
	{"tok-device-a", "device-a", "2999-01-01 00:00:00", false},
	{"tok-device-b", "device-b", "2999-01-01 00:00:00", false},
	{"tok-numeric", "123", "2999-01-01 00:00:00", false},
	{"tok-unbound", "", "2999-01-01 00:00:00", false},
	{"tok-expired", "device-a", "2001-01-01 00:00:00", false},
	{"tok-revoked", "device-a", "2999-01-01 00:00:00", true},
}

// deviceWorld is the whole console on db with the token rows, and its
// transfers folder in the test's temporary folder.
func deviceWorld(t *testing.T, db *store.DB) *replay.World {
	transfers := filepath.Join(t.TempDir(), "transfers")
	w := replay.NewWorld(t, db, func(cfg *config.Config) { cfg.TransfersDir = transfers })
	for _, row := range deviceTokens {
		revoked := any(row[3])
		if db.Dialect() == store.SQLite {
			revoked = 0
			if row[3].(bool) {
				revoked = 1
			}
		}
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked, token_kind) VALUES (?, ?, 5, ?, '', ?, ?, 'device')",
			[]any{row[0], authn.HashAccessToken(row[0].(string)), row[1], row[2], revoked})
	}
	return w
}

var (
	announcedID = regexp.MustCompile(`"transfer_id":"([0-9a-f]{32})"`)
	createdID   = regexp.MustCompile(`^\{"id":"([0-9a-f]{32})"`)
)

// refusedVectors are the vectors of device_vectors.json that a bug fix of
// BUGS.md changed into the device guard's refusal, 403 {"error":"Device
// ID mismatch"}, with the bug: only text names a device, where Node
// compared the text String() made of a number or a list.
var refusedVectors = map[int]string{47: "DV4", 48: "DV4"}

// TestDeviceVectors replays what the Node console answered on the device
// routes, POST /file-transfer and GET /file-transfer/{id} at /api/nd and
// /api/files, with the routes for people of the same file on the same
// transfers (testdata/device_vectors.json): who may call, the device a
// request names, every action, a file a device announces and sends and
// the console downloads, another device writing into it and cancelling it
// (BUGS FT2), and a transfer someone starts that the device answers. The
// whole console runs on SQLite and on PostgreSQL; the Node run had no
// CSRF gate, and the replay sends the CSRF token on every write. The
// vectors in refusedVectors are compared with the refusal.
func TestDeviceVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "device_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []deviceVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 100 {
		t.Fatalf("%d vectors, the file should hold more than 100", len(vectors))
	}
	fixDeviceVectors(t, vectors)
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		ids := &world{goID: map[string]string{}, nodeID: map[string]string{}}
		failures := 0
		for i, v := range vectors {
			headers := map[string]string{}
			for name, value := range v.Request.Headers {
				headers[name] = value
			}
			if v.Request.Type != "" {
				headers["Content-Type"] = v.Request.Type
			}
			req := replay.Request{Role: v.Request.Role, Method: v.Request.Method, Path: ids.local(v.Request.Path), Headers: headers}
			if v.Request.Body != nil {
				body := ids.local(*v.Request.Body)
				req.Body = &body
			}
			got := w.Do(req)
			want, _ := base64.StdEncoding.DecodeString(v.Body)
			// A list is ordered by the millisecond a transfer was made in.
			// The recording made its transfers milliseconds apart, so no
			// two of this run may share one either.
			time.Sleep(2 * time.Millisecond)
			if bug, ok := refusedVectors[i]; ok {
				if got.Status != 403 || got.Body != `{"error":"Device ID mismatch"}` {
					failures++
					t.Errorf("vector %d (%s): %d %s, want the refusal of another device", i, bug, got.Status, got.Body)
				}
				continue
			}

			// A new transfer: remember which id of this run stands for it
			for _, pattern := range []*regexp.Regexp{announcedID, createdID} {
				theirs, ours := pattern.FindStringSubmatch(string(want)), pattern.FindStringSubmatch(got.Body)
				if theirs != nil && ours != nil {
					ids.goID[theirs[1]], ids.nodeID[ours[1]] = ours[1], theirs[1]
				}
			}

			var problems []string
			if got.Status != v.Status {
				problems = append(problems, "status "+strconv.Itoa(got.Status)+", want "+strconv.Itoa(v.Status))
			}
			for _, name := range []string{"content-type", "content-disposition", "accept-ranges", "content-range"} {
				if have := got.Header.Get(name); have != v.Headers[name] {
					problems = append(problems, name+" "+strconv.Quote(have)+", want "+strconv.Quote(v.Headers[name]))
				}
			}
			gotText, wantText := got.Body, string(want)
			if strings.HasPrefix(v.Headers["content-type"], "application/json") {
				gotText = ids.normalize(gotText)
				wantText = tempPath.ReplaceAllString(stamps.ReplaceAllString(wantText, `"$1":0`), `"tempPath":"<DIR>/$1"`)
			}
			if gotText != wantText {
				problems = append(problems, "body "+around(gotText, wantText)+"\n      want "+around(wantText, gotText))
			}
			if len(problems) > 0 {
				failures++
				if failures <= 15 {
					body := ""
					if v.Request.Body != nil {
						body = " " + *v.Request.Body
					}
					t.Errorf("vector %d: %s %s%s as %q with %v:\n  %s", i, v.Request.Method, v.Request.Path, body, v.Request.Role, v.Request.Headers, strings.Join(problems, "\n  "))
				}
			}
		}
		if failures > 15 {
			t.Errorf("%d of %d vectors differ; the first 15 are shown", failures, len(vectors))
		}
	})
}

// A transfer someone starts in the console is the one the device answers,
// and the file it sends is what the console downloads: the routes for
// people and the device routes share the console's transfers (BUGS FT1).
func TestPeopleAndDevicesShareTheTransfers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		device := map[string]string{"Authorization": "Bearer tok-device-a"}
		started := w.Do(replay.Request{Role: "operator", Method: "POST", Path: "/api/files/transfer",
			Body: replay.Text(`{"direction":"download","device_id":"device-a","filename":"log.txt","size":11}`)})
		m := createdID.FindStringSubmatch(started.Body)
		if started.Status != 201 || m == nil {
			t.Fatalf("start: %d %s", started.Status, started.Body)
		}
		id := m[1]
		if got := w.Do(replay.Request{Path: "/api/nd/file-transfer/" + id, Headers: device}); got.Status != 200 || !strings.Contains(got.Body, `"status":"pending"`) {
			t.Fatalf("the device reads the transfer: %d %s", got.Status, got.Body)
		}
		for i, part := range []string{"hello ", "world"} {
			body := `{"action":"chunk","transfer_id":"` + id + `","chunk_index":` + strconv.Itoa(i) + `,"data":"` + base64.StdEncoding.EncodeToString([]byte(part)) + `"}`
			if got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/file-transfer", Body: &body, Headers: device, NoCSRF: true}); got.Status != 200 || !strings.Contains(got.Body, `"action":"chunk_ack"`) {
				t.Fatalf("chunk %d: %d %s", i, got.Status, got.Body)
			}
		}
		if got := w.Do(replay.Request{Role: "operator", Path: "/api/files/transfers?device_id=device-a&status=completed"}); got.Status != 200 || !strings.Contains(got.Body, `"id":"`+id+`"`) {
			t.Errorf("the list of completed transfers: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Role: "operator", Path: "/api/files/transfers/" + id + "/download"}); got.Status != 200 || got.Body != "hello world" {
			t.Errorf("download: %d %q", got.Status, got.Body)
		}
	})
}

// A device with a token writes into and cancels its own transfers only:
// another device's chunk and cancel are refused as ones for no transfer
// (BUGS FT2).
func TestADeviceReachesOnlyItsOwnTransfers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		post := func(token, body string) replay.Answer {
			return w.Do(replay.Request{Method: "POST", Path: "/api/nd/file-transfer", Body: &body, Headers: map[string]string{"Authorization": "Bearer " + token}, NoCSRF: true})
		}
		m := announcedID.FindStringSubmatch(post("tok-device-a", `{"action":"init","filename":"a.txt","size":4}`).Body)
		if m == nil {
			t.Fatal("device-a announced no transfer")
		}
		id := m[1]
		notFound := `{"action":"error","error":"Transfer not found"}`
		if got := post("tok-device-b", `{"action":"chunk","transfer_id":"`+id+`","chunk_index":0,"data":"YWJjZA=="}`); got.Status != 200 || got.Body != notFound {
			t.Errorf("device-b writes into device-a's transfer: %d %s", got.Status, got.Body)
		}
		if got := post("tok-device-b", `{"action":"cancel","transfer_id":"`+id+`"}`); got.Status != 200 || got.Body != notFound {
			t.Errorf("device-b cancels it: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Path: "/api/nd/file-transfer/" + id, Headers: map[string]string{"Authorization": "Bearer tok-device-a"}}); !strings.Contains(got.Body, `"status":"pending","bytesTransferred":0`) {
			t.Errorf("device-a's transfer after that: %d %s", got.Status, got.Body)
		}
		if got := post("tok-device-a", `{"action":"chunk","transfer_id":"`+id+`","chunk_index":0,"data":"YWJjZA=="}`); !strings.Contains(got.Body, `"status":"completed"`) {
			t.Errorf("device-a writes into its transfer: %d %s", got.Status, got.Body)
		}
		// Reading stays with the transfer's device
		if got := w.Do(replay.Request{Path: "/api/nd/file-transfer/" + id, Headers: map[string]string{"Authorization": "Bearer tok-device-b"}}); got.Status != 403 || got.Body != `{"error":"Access denied"}` {
			t.Errorf("device-b reads it: %d %s", got.Status, got.Body)
		}
	})
}

// A device that writes with its token and no session needs no CSRF token
// under /api/nd or /api/files (BUGS DV2). With a session it needs one, like
// a browser.
func TestTheCSRFGateAtEachMount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		device := map[string]string{"Authorization": "Bearer tok-device-a"}
		body := `{"action":"browse","path":"/"}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/file-transfer", Body: &body, Headers: device, NoCSRF: true}); got.Status != 200 {
			t.Errorf("/api/nd without the CSRF token: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/files/file-transfer", Body: &body, Headers: device, NoCSRF: true}); got.Status != 200 {
			t.Errorf("/api/files without the CSRF token: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/files/file-transfer", Body: &body, Headers: device}); got.Status != 200 {
			t.Errorf("/api/files with the CSRF token: %d %s", got.Status, got.Body)
		}
		// With a session the gate checks under /api/nd too
		if got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/nd/file-transfer", Body: &body, Headers: device, NoCSRF: true}); got.Status != 403 {
			t.Errorf("/api/nd with a session and without the CSRF token: %d %s", got.Status, got.Body)
		}
	})
}

// A start beyond the open transfers a device may have is refused with 429
// and the cap's error, by the people's route and by the device's init;
// the transfer is not stored (BUGS RV48, DECISIONS A100).
func TestAStartBeyondTheCapIsTooManyRequests(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		start := func() replay.Answer {
			return w.Do(replay.Request{Role: "operator", Method: "POST", Path: "/api/files/transfer",
				Body: replay.Text(`{"direction":"download","device_id":"device-a","filename":"log.txt","size":11}`)})
		}
		initMessage := func(path string) replay.Answer {
			body := `{"action":"init","filename":"a.txt","size":4}`
			return w.Do(replay.Request{Method: "POST", Path: path, Body: &body, Headers: map[string]string{"Authorization": "Bearer tok-device-a"}, NoCSRF: true})
		}
		for i := 0; i < 5; i++ {
			if got := start(); got.Status != 201 {
				t.Fatalf("start %d: %d %s", i+1, got.Status, got.Body)
			}
			if got := initMessage("/api/nd/file-transfer"); got.Status != 200 || !strings.Contains(got.Body, `"action":"init_ack"`) {
				t.Fatalf("init %d: %d %s", i+1, got.Status, got.Body)
			}
		}
		refusal := "Too many open file transfers for this device (at most 10 pending or active)"
		if got := start(); got.Status != 429 || got.Body != `{"error":"`+refusal+`"}` {
			t.Errorf("start 11: %d %s", got.Status, got.Body)
		}
		for _, path := range []string{"/api/nd/file-transfer", "/api/files/file-transfer"} {
			if got := initMessage(path); got.Status != 429 || got.Body != `{"action":"error","error":"`+refusal+`"}` {
				t.Errorf("init 11 at %s: %d %s", path, got.Status, got.Body)
			}
		}
		if got := w.Do(replay.Request{Role: "operator", Path: "/api/files/transfers?device_id=device-a"}); strings.Count(got.Body, `"id":"`) != 10 {
			t.Errorf("transfers of device-a after the refusals: %s", got.Body)
		}
		// Another failure of a message stays 200
		body := `{"action":"init","filename":"a.txt","size":-1}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/file-transfer", Body: &body, Headers: map[string]string{"Authorization": "Bearer tok-device-b"}, NoCSRF: true}); got.Status != 200 || !strings.Contains(got.Body, `"action":"error"`) {
			t.Errorf("an init with a negative size: %d %s", got.Status, got.Body)
		}
	})
}
