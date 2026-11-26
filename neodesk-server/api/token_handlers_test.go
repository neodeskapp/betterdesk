package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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

// With ENROLLMENT_MODE_ENV_OVERRIDE=Y the mode comes from the environment
// at every start: a read says so and a change is refused, where it was
// saved and undone by the next restart (BUGS L386).
func TestEnrollmentModeForcedByTheEnvironment(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	srv.cfg.EnrollmentMode, srv.cfg.EnrollmentModeEnvOverride = "managed", true
	status, body := asConsole(t, srv, http.MethodGet, "/api/enrollment/mode", "")
	var read struct {
		Mode        string `json:"mode"`
		EnvOverride bool   `json:"env_override"`
	}
	if err := json.Unmarshal([]byte(body), &read); status != http.StatusOK || err != nil || read.Mode != "managed" || !read.EnvOverride {
		t.Errorf("read: %d %s", status, body)
	}
	status, body = asConsole(t, srv, http.MethodPut, "/api/enrollment/mode", `{"mode":"open"}`)
	if status != http.StatusConflict {
		t.Errorf("change: %d %s", status, body)
	}
	if srv.cfg.EnrollmentMode != "managed" {
		t.Errorf("the mode changed to %s", srv.cfg.EnrollmentMode)
	}
	srv.cfg.EnrollmentModeEnvOverride = false
	if status, body = asConsole(t, srv, http.MethodPut, "/api/enrollment/mode", `{"mode":"open"}`); status != http.StatusOK {
		t.Errorf("change without the override: %d %s", status, body)
	}
}

// A read of the enrollment mode says where the mode in force comes from,
// as the start resolves it: the environment when it is forced, the mode
// saved in the console, or the built-in default.
func TestEnrollmentModeSaysWhereItComesFrom(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	source := func() string {
		t.Helper()
		status, body := asConsole(t, srv, http.MethodGet, "/api/enrollment/mode", "")
		var read struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal([]byte(body), &read); status != http.StatusOK || err != nil {
			t.Fatalf("read: %d %s", status, body)
		}
		return read.Source
	}
	srv.cfg.EnrollmentMode = "open"
	if got := source(); got != "configuration" {
		t.Errorf("nothing saved: source %q, want configuration", got)
	}
	if status, body := asConsole(t, srv, http.MethodPut, "/api/enrollment/mode", `{"mode":"managed"}`); status != http.StatusOK {
		t.Fatalf("change: %d %s", status, body)
	}
	if got := source(); got != "database" {
		t.Errorf("saved in the console: source %q, want database", got)
	}
	srv.cfg.EnrollmentModeEnvOverride = true
	if got := source(); got != "environment" {
		t.Errorf("forced by the environment: source %q, want environment", got)
	}
}

// TestDeviceTokenRefusesNegativeUses: Uses allowed below 0 is refused with
// a reason on create, edit and bulk create, and nothing is stored; an edit
// keeps the limit it had (BUGS L380).
func TestDeviceTokenRefusesNegativeUses(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	const reason = "Uses allowed must be a whole number of 0 or more"
	status, body := asConsole(t, srv, http.MethodPost, "/api/tokens", `{"name":"bad","max_uses":-2}`)
	if status != http.StatusBadRequest || !strings.Contains(body, reason) {
		t.Errorf("create with -2: %d %s, want 400 %q", status, body, reason)
	}
	status, body = asConsole(t, srv, http.MethodPost, "/api/tokens/generate-bulk", `{"count":2,"max_uses":-3}`)
	if status != http.StatusBadRequest || !strings.Contains(body, reason) {
		t.Errorf("bulk with -3: %d %s, want 400 %q", status, body, reason)
	}
	_, body = asConsole(t, srv, http.MethodGet, "/api/tokens", "")
	if strings.Contains(body, `"bad"`) || strings.Contains(body, `"max_uses":-`) {
		t.Errorf("a refused token was stored: %s", body)
	}

	status, body = asConsole(t, srv, http.MethodPost, "/api/tokens", `{"name":"good","max_uses":2}`)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); status != http.StatusCreated || err != nil {
		t.Fatalf("create: %d %s", status, body)
	}
	path := "/api/tokens/" + strconv.FormatInt(created.ID, 10)
	status, body = asConsole(t, srv, http.MethodPut, path, `{"max_uses":-1}`)
	if status != http.StatusBadRequest || !strings.Contains(body, reason) {
		t.Errorf("edit with -1: %d %s, want 400 %q", status, body, reason)
	}
	_, body = asConsole(t, srv, http.MethodGet, path, "")
	if !strings.Contains(body, `"max_uses":2`) {
		t.Errorf("after a refused edit: %s, want max_uses 2", body)
	}
}
