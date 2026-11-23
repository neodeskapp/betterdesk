package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestOrganizationValuesAreChecked: an organization's name, logo address,
// new accounts, assigned devices and invitation addresses are checked
// before they are stored; a refusal is 400 (404 for a device the server
// doesn't know) with a reason, and nothing changes (BUGS L398).
func TestOrganizationValuesAreChecked(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateOrganization(&db.Organization{ID: "org-v", Name: "Values", Slug: "values", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPeer(&db.Peer{ID: "900200001", Hostname: "known", Status: "OFFLINE"}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("n", 121)
	for _, c := range []struct {
		method, path, body string
		want               int
		reason             string
	}{
		{http.MethodPost, "/api/org", `{"name":"` + long + `","slug":"long-one"}`, http.StatusBadRequest, "120"},
		{http.MethodPost, "/api/org", `{"name":"Logo","slug":"logo-one","logo_url":"javascript:alert(1)"}`, http.StatusBadRequest, "logo_url"},
		{http.MethodPut, "/api/org/org-v", `{"name":"  "}`, http.StatusBadRequest, "name is required"},
		{http.MethodPut, "/api/org/org-v", `{"name":"` + long + `"}`, http.StatusBadRequest, "120"},
		{http.MethodPut, "/api/org/org-v", `{"logo_url":"//evil.example/x.png"}`, http.StatusBadRequest, "logo_url"},
		{http.MethodPost, "/api/org/org-v/users", `{"username":"a b","password":"eight888"}`, http.StatusBadRequest, "username"},
		{http.MethodPost, "/api/org/org-v/devices", `{"device_id":"nope-device"}`, http.StatusNotFound, "device"},
		{http.MethodPost, "/api/org/org-v/invite", `{"email":"not-mail"}`, http.StatusBadRequest, "email"},
	} {
		status, answer := asConsole(t, srv, c.method, c.path, c.body)
		if status != c.want || !strings.Contains(answer, c.reason) {
			t.Errorf("%s %s %s: %d %s, want %d naming %q", c.method, c.path, c.body, status, answer, c.want, c.reason)
		}
	}
	org, err := database.GetOrganization("org-v")
	if err != nil || org == nil || org.Name != "Values" || org.LogoURL != "" {
		t.Errorf("after refused edits the organization is %+v (%v), want it unchanged", org, err)
	}
	if devices, _ := database.ListOrgDevices("org-v"); len(devices) != 0 {
		t.Errorf("a refused device was assigned: %+v", devices)
	}

	// What is right still works
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPut, "/api/org/org-v", `{"name":"Values Ltd","logo_url":"https://cdn.example.com/logo.png"}`, http.StatusOK},
		{http.MethodPut, "/api/org/org-v", `{"logo_url":""}`, http.StatusOK},
		{http.MethodPost, "/api/org/org-v/users", `{"username":"a.b@values","password":"eight888"}`, http.StatusCreated},
		{http.MethodPost, "/api/org/org-v/devices", `{"device_id":"900200001"}`, http.StatusCreated},
		{http.MethodPost, "/api/org/org-v/invite", `{"email":"someone@example.org"}`, http.StatusCreated},
		{http.MethodPost, "/api/org/org-v/invite", `{}`, http.StatusCreated},
	} {
		if status, answer := asConsole(t, srv, c.method, c.path, c.body); status != c.want {
			t.Errorf("%s %s %s: %d %s, want %d", c.method, c.path, c.body, status, answer, c.want)
		}
	}
}
