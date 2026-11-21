package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// TestCreateDeviceTokenAnswersPrefixAndTime: a new token, one by one or in
// bulk, is answered as a later read shows it, with its stored creation time
// and the eight-character prefix, plus the whole token once (BUGS G13).
func TestCreateDeviceTokenAnswersPrefixAndTime(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	type token struct {
		ID          int64  `json:"id"`
		Token       string `json:"token"`
		TokenPrefix string `json:"token_prefix"`
		CreatedAt   string `json:"created_at"`
		ExpiresAt   string `json:"expires_at"`
	}
	check := func(what string, got token) {
		t.Helper()
		if len(got.Token) != 32 || got.TokenPrefix != got.Token[:8] {
			t.Errorf("%s: token %q, prefix %q", what, got.Token, got.TokenPrefix)
		}
		status, body := asConsole(t, srv, http.MethodGet, "/api/tokens/"+strconv.FormatInt(got.ID, 10), "")
		var read token
		if err := json.Unmarshal([]byte(body), &read); status != http.StatusOK || err != nil {
			t.Fatalf("%s: read back: %d %s", what, status, body)
		}
		if got.CreatedAt != read.CreatedAt || got.CreatedAt == "0001-01-01T00:00:00Z" || got.ExpiresAt != read.ExpiresAt || got.TokenPrefix != read.TokenPrefix {
			t.Errorf("%s: answered created_at %s, expires_at %s, prefix %s; a read shows %s, %s, %s",
				what, got.CreatedAt, got.ExpiresAt, got.TokenPrefix, read.CreatedAt, read.ExpiresAt, read.TokenPrefix)
		}
	}

	status, body := asConsole(t, srv, http.MethodPost, "/api/tokens", `{"name":"one","max_uses":1,"expires_in":3600}`)
	var one token
	if err := json.Unmarshal([]byte(body), &one); status != http.StatusCreated || err != nil {
		t.Fatalf("create: %d %s", status, body)
	}
	check("create", one)

	status, body = asConsole(t, srv, http.MethodPost, "/api/tokens/generate-bulk", `{"count":2,"expires_in":600}`)
	var bulk struct{ Tokens []token }
	if err := json.Unmarshal([]byte(body), &bulk); status != http.StatusCreated || err != nil || len(bulk.Tokens) != 2 {
		t.Fatalf("generate-bulk: %d %s", status, body)
	}
	for _, tok := range bulk.Tokens {
		check("generate-bulk", tok)
	}
}

// TestUpdateDeviceTokenClearsNoteAndLimit: an edit with a note of "" clears
// the note and max_uses 0 makes the token unlimited; fields the body leaves
// out keep their values (RV196).
func TestUpdateDeviceTokenClearsNoteAndLimit(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	status, body := asConsole(t, srv, http.MethodPost, "/api/tokens", `{"name":"bundle","max_uses":5,"note":"for the lab"}`)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); status != http.StatusCreated || err != nil {
		t.Fatalf("create: %d %s", status, body)
	}
	path := "/api/tokens/" + strconv.FormatInt(created.ID, 10)
	type token struct {
		Name    string `json:"name"`
		MaxUses int    `json:"max_uses"`
		Note    string `json:"note"`
	}
	read := func() token {
		t.Helper()
		_, body := asConsole(t, srv, http.MethodGet, path, "")
		var got token
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("read %s: %v", body, err)
		}
		return got
	}

	if status, body := asConsole(t, srv, http.MethodPut, path, `{"name":"renamed"}`); status != http.StatusOK {
		t.Fatalf("rename: %d %s", status, body)
	}
	if got := read(); got != (token{"renamed", 5, "for the lab"}) {
		t.Errorf("after a rename: %+v, want the limit and note kept", got)
	}
	if status, body := asConsole(t, srv, http.MethodPut, path, `{"max_uses":0,"note":""}`); status != http.StatusOK {
		t.Fatalf("clear: %d %s", status, body)
	}
	if got := read(); got != (token{"renamed", 0, ""}) {
		t.Errorf("after max_uses 0 and an empty note: %+v, want unlimited without a note", got)
	}
}
