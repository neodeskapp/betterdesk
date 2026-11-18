package commercialization_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/commercialization"
)

// The vectors are what routes/commercialization.routes.js of the Node
// console answered and asked the API server for the routes that pass a
// call on (testdata/nodeoracle/run.js): every role, every kind of answer
// of the API server (money and times included), the files of the
// exports, path parameters, queries and bodies. They are replayed in two
// parts: the calls the API server answers, and the ones it never answers,
// which each wait for the call to give up.

func TestVectorsAnswered(t *testing.T) {
	replay(t, func(v proxytest.Vector) bool { return v.API == nil || !v.API.Hang })
}

func TestVectorsUnanswered(t *testing.T) {
	replay(t, func(v proxytest.Vector) bool { return v.API != nil && v.API.Hang })
}

// TestVectorsQuick is a part of the answered vectors that takes seconds:
// every route for every role (the first 147), every query that cannot be
// written, and every fifteenth of the others. Mutation runs use it.
func TestVectorsQuick(t *testing.T) {
	i := -1
	replay(t, func(v proxytest.Vector) bool {
		i++
		if v.API != nil && v.API.Hang {
			return false
		}
		return i < 147 || strings.Contains(v.Request.Path, "toString") || i%15 == 0
	})
}

func replay(t *testing.T, keep func(proxytest.Vector) bool) {
	t.Helper()
	var kept []proxytest.Vector
	for _, v := range proxytest.Load(t, filepath.Join("testdata", "vectors.json")) {
		if keep(v) {
			kept = append(kept, v)
		}
	}
	data, err := json.Marshal(kept)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "vectors.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	proxytest.Replay(t, file, commercialization.Register)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true)}
	commercialization.Register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)
	routes := []string{
		"GET /api/panel/billing/timesync/status",
		"POST /api/panel/billing/timesync/check",
		"GET /api/panel/billing/clock/settings",
		"PUT /api/panel/billing/clock/settings",
		"GET /api/panel/billing/clock/detect-timezone",
		"GET /api/panel/billing/packages",
		"GET /api/panel/billing/packages/{packageId}/preview",
		"POST /api/panel/billing/packages",
		"PUT /api/panel/billing/packages/{packageId}",
		"DELETE /api/panel/billing/packages/{packageId}",
		"GET /api/panel/billing/contracts",
		"POST /api/panel/billing/contracts",
		"PUT /api/panel/billing/contracts/{contractId}",
		"DELETE /api/panel/billing/contracts/{contractId}",
		"GET /api/panel/billing/stats",
		"GET /api/panel/billing/sessions",
		"GET /api/panel/billing/check",
		"GET /api/panel/billing/sessions/pending",
		"GET /api/panel/billing/sessions/export",
		"GET /api/panel/billing/reports",
		"GET /api/panel/billing/reports/export",
		"POST /api/panel/billing/sessions/{sessionId}/report",
		"GET /api/panel/billing/currencies",
		"PUT /api/panel/billing/currencies/{currencyCode}",
		"GET /api/panel/commercialization/email-config",
		"PUT /api/panel/commercialization/email-config",
		"GET /api/panel/commercialization/smtp-status",
	}
	sort.Strings(routes)
	if strings.Join(got, "\n") != strings.Join(routes, "\n") {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(routes, "\n"))
	}
}
