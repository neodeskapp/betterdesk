package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestPatchPeerTakesTagsAsList: PATCH /api/peers/{id} takes tags as a text
// or as a list of texts, as PUT /api/peers/{id}/tags does; any other value
// is refused (BUGS G19).
func TestPatchPeerTakesTagsAsList(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, c := range []struct {
		body   string
		status int
		tags   string
	}{
		{`{"tags":["office","printer"]}`, http.StatusOK, "office,printer"},
		{`{"tags":"lab"}`, http.StatusOK, "lab"},
		{`{"tags":[]}`, http.StatusOK, ""},
		{`{"tags":5}`, http.StatusBadRequest, ""},
		{`{"tags":[1]}`, http.StatusBadRequest, ""},
	} {
		status, body := asConsole(t, srv, http.MethodPatch, "/api/peers/PEERAAA1", c.body)
		if status != c.status {
			t.Errorf("PATCH %s: %d %s, want %d", c.body, status, body, c.status)
		}
		p, err := database.GetPeer("PEERAAA1")
		if err != nil || p == nil {
			t.Fatalf("GetPeer: %v %v", p, err)
		}
		if p.Tags != c.tags {
			t.Errorf("tags after PATCH %s: %q, want %q", c.body, p.Tags, c.tags)
		}
	}
}

// TestPatchPeerLinksAPeer: PATCH /api/peers/{id} takes linked_peer_id, so
// the console's link route works: the peer then shows in the other one's
// linked list, and "" removes the link (BUGS RC6).
func TestPatchPeerLinksAPeer(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	linked := func() string {
		t.Helper()
		status, body := asConsole(t, srv, http.MethodGet, "/api/peers/PEERBBB2/linked", "")
		if status != http.StatusOK {
			t.Fatalf("GET linked: %d %s", status, body)
		}
		var answer struct {
			Linked []struct {
				ID string `json:"id"`
			} `json:"linked"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(body), &answer); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(answer.Linked))
		for _, p := range answer.Linked {
			ids = append(ids, p.ID)
		}
		return fmt.Sprintf("%d %s", answer.Total, strings.Join(ids, ","))
	}
	for _, c := range []struct {
		body   string
		status int
		linked string
	}{
		{`{"linked_peer_id":"PEERBBB2"}`, http.StatusOK, "1 PEERAAA1"},
		{`{"linked_peer_id":""}`, http.StatusOK, "0 "},
		{`{"linked_peer_id":["PEERBBB2"]}`, http.StatusBadRequest, "0 "},
		{`{"linked_peer_id":null}`, http.StatusBadRequest, "0 "},
	} {
		status, body := asConsole(t, srv, http.MethodPatch, "/api/peers/PEERAAA1", c.body)
		if status != c.status {
			t.Errorf("PATCH %s: %d %s, want %d", c.body, status, body, c.status)
		}
		if got := linked(); got != c.linked {
			t.Errorf("linked after PATCH %s: %q, want %q", c.body, got, c.linked)
		}
	}
	p, err := database.GetPeer("PEERAAA1")
	if err != nil || p == nil || p.LinkedPeerID != "" {
		t.Errorf("PEERAAA1 after the clear: %+v %v", p, err)
	}
}
