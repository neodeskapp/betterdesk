package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/audit"
	"github.com/unitronix/betterdesk-server/auth"
)

// TestServerEventsNameTheAccount: a ban and a change of the configuration
// the console makes in process name the account it acts for, and one over
// the listener names the API key, not 127.0.0.1 or the caller's address
// (L91).
func TestServerEventsNameTheAccount(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	log := audit.NewLogger("")
	log.UseStore(database)
	t.Cleanup(func() { log.Close() })
	srv.SetAuditLogger(log)
	handler := srv.InternalHandler()
	call := func(method, path, body string, id Identity) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:0"
		req = req.WithContext(WithIdentity(req.Context(), id))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
	}
	bridge := Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin, Actor: "alice"}
	call(http.MethodPost, "/api/peers/PEERAAA1/ban", `{"reason":"spam"}`, bridge)
	call(http.MethodPut, "/api/config/some_key", `{"value":"1"}`, bridge)
	call(http.MethodPost, "/api/peers/PEERBBB2/ban", `{"reason":"spam"}`, Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin})

	events := log.Recent(3)
	if len(events) != 3 {
		t.Fatalf("events %+v", events)
	}
	for i, want := range []struct{ action, actor string }{
		{"peer_banned", "apikey:console-bridge"}, {"config_changed", "alice"}, {"peer_banned", "alice"},
	} {
		if string(events[i].Action) != want.action || events[i].Actor != want.actor {
			t.Errorf("event %d: %s by %q, want %s by %q", i, events[i].Action, events[i].Actor, want.action, want.actor)
		}
	}
}

// TestAuditEventsPageFromTheOffset: /api/audit/events takes the offset,
// and its total counts the events of the filter (L126).
func TestAuditEventsPageFromTheOffset(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	log := audit.NewLogger("")
	log.UseStore(database)
	t.Cleanup(func() { log.Close() })
	srv.SetAuditLogger(log)
	for _, target := range []string{"a", "b", "c", "d"} {
		log.Log(audit.ActionPeerBanned, "alice", target, nil)
	}
	log.Log(audit.ActionConfigChanged, "alice", "k", nil)
	handler := srv.InternalHandler()
	get := func(query string) (total int, targets []string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/audit/events?"+query, nil)
		req.RemoteAddr = "127.0.0.1:0"
		req = req.WithContext(WithIdentity(req.Context(), Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var body struct {
			Total  int `json:"total"`
			Events []struct {
				Target string `json:"target"`
			} `json:"events"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v %s", query, err, rec.Body.String())
		}
		for _, e := range body.Events {
			targets = append(targets, e.Target)
		}
		return body.Total, targets
	}
	if total, targets := get("limit=2&offset=1&action=peer_banned"); total != 4 || strings.Join(targets, ",") != "c,b" {
		t.Errorf("bans from the second: total %d, %v", total, targets)
	}
	if total, targets := get("limit=2&offset=4"); total != 5 || strings.Join(targets, ",") != "a" {
		t.Errorf("all from the fifth: total %d, %v", total, targets)
	}
	if _, targets := get("limit=1&offset=bogus"); strings.Join(targets, ",") != "k" {
		t.Errorf("an offset that is no number: %v", targets)
	}
}
