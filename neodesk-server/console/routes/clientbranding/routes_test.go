package clientbranding_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

func newWorld(t *testing.T) (*routetest.World, *routetest.API) {
	t.Helper()
	api := routetest.NewAPI()
	return routetest.New(t, routetest.Options{API: api}), api
}

// Both routes need a session and branding.edit; the client branding is in
// no panel section, so global_admin may use it.
func TestGuards(t *testing.T) {
	w, api := newWorld(t)
	api.On("GET /api/branding", http.StatusOK, `{"company_name":"Co"}`)
	api.On("POST /api/branding", http.StatusOK, `{"success":true}`)
	for _, c := range []struct{ method, body string }{{"GET", ""}, {"POST", "{}"}} {
		if res := w.SignedOut().Do(c.method, "/api/client-branding", c.body); res.Status != http.StatusUnauthorized || res.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
			t.Errorf("%s signed out: %d %s", c.method, res.Status, res.Body)
		}
		for _, role := range []string{"viewer", "operator", "server_admin"} {
			if res := w.As(role).Do(c.method, "/api/client-branding", c.body); res.Status != http.StatusForbidden || res.Body != `{"success":false,"error":"Permission denied: branding.edit"}` {
				t.Errorf("%s as %s: %d %s", c.method, role, res.Status, res.Body)
			}
		}
		for _, role := range []string{"global_admin", "admin"} {
			if res := w.As(role).Do(c.method, "/api/client-branding", c.body); res.Status != http.StatusOK {
				t.Errorf("%s as %s: %d %s", c.method, role, res.Status, res.Body)
			}
		}
	}
	for _, call := range api.Calls() {
		if !strings.HasPrefix(call, "GET /api/branding") && !strings.HasPrefix(call, "POST /api/branding") {
			t.Errorf("unexpected call %s", call)
		}
	}
}

func TestGet(t *testing.T) {
	w, api := newWorld(t)
	admin := w.As("admin")
	api.On("GET /api/branding", http.StatusOK, `{"schema_version":1,"revision":"7","company_name":"Co"}`)
	if res := admin.Do("GET", "/api/client-branding", ""); res.Body != `{"success":true,"data":{"schema_version":1,"revision":"7","company_name":"Co"}}` {
		t.Errorf("get: %d %s", res.Status, res.Body)
	}
	// a refused call is 502 with the client's text, not the server's
	api.On("GET /api/branding", http.StatusInternalServerError, `{"error":"broken"}`)
	if res := admin.Do("GET", "/api/client-branding", ""); res.Status != http.StatusBadGateway || res.Body != `{"success":false,"error":"Request failed with status code 500"}` {
		t.Errorf("refused: %d %s", res.Status, res.Body)
	}
}

func TestSave(t *testing.T) {
	w, api := newWorld(t)
	admin := w.As("admin")
	api.On("POST /api/branding", http.StatusOK, `{"branding":{"company_name":"Co","revision":"123"},"revision":"123","success":true}`)
	res := admin.Do("POST", "/api/client-branding", `{"colors":{"a":"#fff"},"company_name":"Co","phone":null,"unknown":1,"logo":{"mime":"image/png"},"accent_color":"#123"}`)
	if res.Body != `{"success":true,"data":{"branding":{"company_name":"Co","revision":"123"},"revision":"123","success":true},"revision":"123"}` {
		t.Errorf("save: %d %s", res.Status, res.Body)
	}
	// the fields in their fixed order, null kept, unknown keys left out,
	// the logo last
	if calls := api.Calls(); len(calls) != 1 || calls[0] != `POST /api/branding {"company_name":"Co","phone":null,"accent_color":"#123","colors":{"a":"#fff"},"logo":{"mime":"image/png"}}` {
		t.Errorf("sent: %v", calls)
	}
	for body, sent := range map[string]string{
		`{"clear_logo":1,"logo":{"mime":"image/png"}}`: `{"clear_logo":true}`,
		`{"clear_logo":0,"logo":""}`:                   `{}`,
		`[1,2]`:                                        `{}`,
		`{}`:                                           `{}`,
	} {
		admin.Do("POST", "/api/client-branding", body)
		if calls := api.Calls(); len(calls) != 1 || calls[0] != "POST /api/branding "+sent {
			t.Errorf("%s sent %v, want %s", body, calls, sent)
		}
	}
	// without a revision in the answer the key is left out
	api.On("POST /api/branding", http.StatusOK, `{"success":true,"revision":""}`)
	if res := admin.Do("POST", "/api/client-branding", `{}`); res.Body != `{"success":true,"data":{"success":true,"revision":""}}` {
		t.Errorf("no revision: %s", res.Body)
	}
	api.OnText("POST /api/branding", http.StatusBadRequest, "accent_color must be #RGB or #RRGGBB\n")
	if res := admin.Do("POST", "/api/client-branding", `{"accent_color":"blue"}`); res.Status != http.StatusBadGateway || res.Body != `{"success":false,"error":"Request failed with status code 400"}` {
		t.Errorf("refused: %d %s", res.Status, res.Body)
	}
	// an answer with an error key is a failure, and an empty error the
	// route's own text
	api.On("POST /api/branding", http.StatusOK, `{"error":""}`)
	if res := admin.Do("POST", "/api/client-branding", `{}`); res.Status != http.StatusBadGateway || res.Body != `{"success":false,"error":"Failed to save branding on NeoDesk server"}` {
		t.Errorf("error key: %d %s", res.Status, res.Body)
	}
	if res := admin.Do("POST", "/api/client-branding", "company_name=x", "Content-Type", "application/x-www-form-urlencoded"); res.Status != http.StatusUnsupportedMediaType {
		t.Errorf("a form: %d %s", res.Status, res.Body)
	}
}
