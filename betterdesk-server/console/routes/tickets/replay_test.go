package tickets_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// uploads is the folder the console of a test world keeps attachments in.
func uploads(w *replay.World) string {
	return filepath.Join(w.Config.ConsoleDir, "data", "attachments")
}

var randomPart = regexp.MustCompile(`^([0-9]+)_[0-9a-f]{16}_`)

// storedFiles lists the attachment folder with the random part of each
// name replaced.
func storedFiles(t *testing.T, w *replay.World) []string {
	t.Helper()
	entries, err := os.ReadDir(uploads(w))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, randomPart.ReplaceAllString(entry.Name(), "${1}_<random>_"))
	}
	sort.Strings(names)
	return names
}

// TestNodeAnswers replays what the Node console's ticket routes answered
// with its real database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for every role, lists with every filter, every field with values of
// every JSON type, uploads in every form Buffer.from takes, downloads
// with their bytes and headers, the audit log, and the files that are
// left at the end.
//
// The recording was made without the panel access guard, which sits in
// front of the routes in the console. Where the guard refuses a write
// that the route itself refused with "Permission denied", the guard's
// answer is taken for it.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Vars["<UPLOADS>"] = uploads(w)
		w.Run(replay.Load(t, db, "node"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			switch {
			case step.Files != nil:
				if have := storedFiles(t, w); strings.Join(have, "\n") != strings.Join(step.Files, "\n") {
					t.Errorf("files left in the attachment folder:\n   node: %q\n     go: %q", step.Files, have)
				}
				return true
			case step.Status == 403 && strings.Contains(step.Answer, "Permission denied: device.edit") && got.Status == 403 && strings.Contains(got.Body, `"code":"panel_access_denied"`):
				return true
			case step.Extra["base64"] != nil:
				download(t, step, got)
				return true
			}
			return false
		})
	})
}

// download compares a file download: status, bytes and the headers that
// do not depend on the time the file was written.
func download(t *testing.T, step replay.Step, got replay.Answer) {
	t.Helper()
	var want struct {
		Base64       string            `json:"base64"`
		Headers      map[string]string `json:"headers"`
		ETag         bool              `json:"etag"`
		LastModified bool              `json:"lastModified"`
	}
	raw, _ := json.Marshal(step.Extra)
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(want.Base64)
	if got.Status != step.Status || replay.Normalize(got.Body) != replay.Normalize(string(body)) {
		t.Errorf("%s %s %v: node %d %q, go %d %q", step.Method, step.Path, step.Headers, step.Status, body, got.Status, got.Body)
	}
	for _, name := range []string{"content-type", "content-disposition", "cache-control", "accept-ranges", "content-range"} {
		// A header reaches a client as Latin-1 text without spaces around it
		have := strings.TrimSpace(httpx.Latin1(got.Header.Get(name)))
		if name == "cache-control" {
			// The pipeline of the console sets its own in front of the route
			continue
		}
		if have != want.Headers[name] {
			t.Errorf("%s %s %v: header %s: node %q, go %q", step.Method, step.Path, step.Headers, name, want.Headers[name], have)
		}
	}
	if hasETag := got.Header.Get("ETag") != ""; hasETag != want.ETag {
		t.Errorf("%s %s: ETag present: node %v, go %v", step.Method, step.Path, want.ETag, hasETag)
	}
	if has := got.Header.Get("Last-Modified") != ""; has != want.LastModified {
		t.Errorf("%s %s: Last-Modified present: node %v, go %v", step.Method, step.Path, want.LastModified, has)
	}
}

// TestNodeDeviceAnswers replays what the device routes of the Node file
// (POST and GET /api/tickets/bd) answered with its real database adapter,
// on SQLite and on PostgreSQL (testdata/node_device_sqlite.json,
// testdata/node_device_postgres.json): tokens in every state, the list of
// a device, tickets created with values of every JSON type, the rows they
// wrote and the time they are due.
func TestNodeDeviceAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Run(replay.Load(t, db, "node_device"), nil)
	})
}

// A device that writes to /api/tickets/bd needs a CSRF token like a
// browser: the mount is not below /api/bd/, where the gate lets requests
// without a session through. The Node console answers the same (contract
// case tickets/device-create-without-csrf-token).
func TestADeviceWriteNeedsTheCSRFToken(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00')",
			[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
		device := map[string]string{"Authorization": "Bearer tok-device-a"}
		refused := `{"success":false,"error":"Invalid CSRF token. Please refresh the page and try again. If you recently disabled SSL, clear your browser cookies for this site and reload."}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/tickets/bd", Body: replay.Text(`{"title":"x"}`), Headers: device, NoCSRF: true}); got.Status != 403 || got.Body != refused {
			t.Errorf("without the token: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/tickets/bd", Body: replay.Text(`{"title":"x"}`), Headers: map[string]string{"Authorization": "Bearer nope"}}); got.Status != 401 {
			t.Errorf("with the CSRF token and an unknown device token: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/tickets/bd", Body: replay.Text(`{"title":"x"}`), Headers: device}); got.Status != 201 || got.Body != `{"success":true,"ticket_id":1}` {
			t.Errorf("with both tokens: %d %s", got.Status, got.Body)
		}
		// A read needs none
		if got := w.Do(replay.Request{Path: "/api/tickets/bd", Headers: device}); got.Status != 200 || !strings.Contains(got.Body, `"total":1`) {
			t.Errorf("the list: %d %s", got.Status, got.Body)
		}
	})
}
