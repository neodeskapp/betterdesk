package api

import (
	"net/http"
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
