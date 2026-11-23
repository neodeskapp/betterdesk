package remote

import (
	"net/http"
	"regexp"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/reqquery"
)

// The dev mock of the viewer stands in for the remote client in the
// browser and draws a made-up desktop, so the viewer can be reviewed
// without a device. Its code is part of the web app
// (web-ui/src/lib/remote/client/mock-session.ts); the server only says in
// the viewer's data whether a request gets it, and only with
// ND_DEV_MOCK_REMOTE=1.

// mockScenarios are the scenarios the mock knows (SCENARIOS).
var mockScenarios = []string{"ok", "slow", "password", "2fa", "fail-relay", "refused", "offline", "timeout", "ended", "dropped", "one-at-a-time"}

// seededDevice is a fake device of the dev seed scripts: IDs 900100001 to
// 900100099 and from 950000000 (isSeededDevice).
var seededDevice = regexp.MustCompile(`^(9001000\d\d|950\d{6})$`)

// mockFor is devMockRemote.forRequest: the mock's settings for a viewer
// request, nil when the mock is off. With the mock on, ?mock=1 asks for
// it and a seeded demo device gets it unless ?mock=0; ?scenario= picks a
// known scenario in any letter case, else "ok". bad names a query value
// that is no text.
func (h *handlers) mockFor(r *http.Request, deviceID string) (settings any, bad string) {
	if !h.src.mockOn {
		return nil, ""
	}
	q := reqquery.Of(r)
	mock, ok := queryText(q, "mock", "")
	if !ok {
		return nil, "mock"
	}
	if mock != "1" && (mock == "0" || !seededDevice.MatchString(deviceID)) {
		return nil, ""
	}
	wanted, ok := queryText(q, "scenario", "ok")
	if !ok {
		return nil, "scenario"
	}
	wanted = jsbind.ToLower(wanted)
	for _, scenario := range mockScenarios {
		if scenario == wanted {
			return mockSettings{Scenario: wanted}, ""
		}
	}
	return mockSettings{Scenario: "ok"}, ""
}

type mockSettings struct {
	Scenario string `json:"scenario"`
}

// queryText is String(req.query[name] || fallback). ok is false where
// JavaScript throws: a value that is an object with a member toString.
func queryText(q *reqquery.Query, name, fallback string) (string, bool) {
	value := q.Get(name)
	if !value.Truthy() {
		return fallback, true
	}
	return value.Text()
}
