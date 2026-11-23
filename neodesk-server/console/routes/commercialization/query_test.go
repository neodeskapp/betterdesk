package commercialization_test

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/commercialization"
)

// The routes that pass the query on leave out the parameters written with
// brackets: no value reaches the API server as a list or as "[object
// Object]", and no query is a server error. The others go on as before, a
// repeated one as its values joined by commas (BUGS CM7).
func TestQueryPassesPlainParametersOnly(t *testing.T) {
	w := proxytest.NewWorld(t, commercialization.Register)
	admin := &proxytest.Account{UserID: 1}
	admin.User.Username, admin.User.Role = "e2e_admin", "admin"
	api := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true,"items":[]}`}
	for query, want := range map[string]string{
		"period=2026-03&limit=5":          "?period=2026-03&limit=5",
		"status=a&status=b":               "?status=a%2Cb",
		"period[x]=a&limit=5":             "?limit=5",
		"period[toString]=x":              "",
		"limit[toString]=1&org_id=7":      "?org_id=7",
		"period%5B%5D=2026-03&org_id=7":   "?org_id=7",
		"period[]=2026-03&period=2026-04": "?period=2026-04",
		"a=[x]=y&b=1":                     "?b=1",
		"a[b][c]=1":                       "",
	} {
		for route, call := range map[string]string{
			"/api/panel/billing/contracts":       "/api/billing/contracts",
			"/api/panel/billing/sessions/export": "/api/billing/sessions/export",
			"/api/panel/billing/reports":         "/api/billing/reports",
		} {
			rec, calls := w.Do(proxytest.Request{Method: "GET", Path: route + "?" + query, Session: admin}, api)
			if rec.Code != 200 || len(calls) != 1 || calls[0].URL != call+want {
				t.Errorf("%s?%s: %d %s, calls %+v; want the call %s", route, query, rec.Code, rec.Body.String(), calls, call+want)
			}
		}
	}
}
