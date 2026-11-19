package cdapproxy

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// oraclePermissions are the roles of the Node oracle (proxy.js).
var oraclePermissions = map[string][]string{
	"admin":        {"cdap.terminal", "cdap.files", "cdap.command"},
	"global_admin": {"cdap.terminal", "cdap.files", "cdap.command"},
	"operator":     {"cdap.command"},
}

// describeHandshake is what the oracle's API server noted of a
// handshake: the path, the subprotocol and the user headers, read as
// Latin-1 as Node read them.
func describeHandshake(r *http.Request) wstest.Event {
	header := func(name string) string {
		values := r.Header.Values(name)
		if len(values) == 0 {
			return "<none>"
		}
		return wstest.FromLatin1([]byte(values[0]))
	}
	return wstest.Event{
		"open":     r.URL.RequestURI(),
		"protocol": r.Header.Get("Sec-WebSocket-Protocol"),
		"user":     header("X-Username"),
		"role":     header("X-Role"),
	}
}

// TestNodeScenarios plays every scenario the Node oracle recorded
// (testdata/node_proxy.json) on the Go proxy, as the bug fixes changed
// it (fixedRecording): the checks and their order, who may open which
// socket, what reaches the API server's socket (path, subprotocol, each
// message with its kind), what comes back, the closes on either side,
// the messages that wait for the handshake, the API server refusing or
// failing.
func TestNodeScenarios(t *testing.T) {
	wstest.PlayProxy(t, fixedRecording(t), wstest.ProxySetup{
		Register: func(reg *ws.Registry, api http.Handler) {
			New(Options{API: api, Devices: knownDevices{}, CanAccess: everyDevice}).Register(reg)
		},
		Permissions: oraclePermissions,
		Describe:    describeHandshake,
	})
}

// openedFor are the steps of the recording where Node's process ended on
// an account's name (BUGS SK2) and the socket opens since the fix: the
// events of the step, in the oracle's notation.
var openedFor = map[string]string{
	"crash: a user name outside Latin-1":          `[{"permission":["admin","cdap.terminal"]},{"socket":"b","head":"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"},{"upstream":"u2","open":"/api/cdap/devices/CDAP-1/terminal","protocol":"cdap-terminal","user":"<none>","role":"<none>"}]`,
	"crash: a user name with a control character": `[{"permission":["admin","cdap.files"]},{"socket":"b","head":"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"},{"upstream":"u2","open":"/api/cdap/devices/CDAP-1/files","protocol":"cdap-filebrowser","user":"<none>","role":"<none>"}]`,
}

// fixedRecording writes the recording as the Go proxy answers it since
// SK2 and returns its path. The account's name and role no longer go to
// the API server (it never read them), so every socket the API server
// opens has neither, and the two accounts whose names ended Node's
// process open their socket.
func fixedRecording(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec wstest.ProxyRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	opened := 0
	for i := range rec.Scenarios {
		sc := &rec.Scenarios[i]
		for j := range sc.Steps {
			step := &sc.Steps[j]
			if events, ok := openedFor[sc.Name]; ok && j == len(sc.Steps)-1 {
				if err := json.Unmarshal([]byte(events), &step.Events); err != nil {
					t.Fatal(err)
				}
				sc.Crash = false
				opened++
				continue
			}
			for k, e := range step.Events {
				var event map[string]any
				if err := json.Unmarshal(e, &event); err != nil {
					t.Fatal(err)
				}
				if event["upstream"] == nil || event["open"] == nil {
					continue
				}
				event["user"], event["role"] = "<none>", "<none>"
				if step.Events[k], err = json.Marshal(event); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if opened != len(openedFor) {
		t.Fatalf("the SK2 steps: %d of %d found in the recording", opened, len(openedFor))
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "node_proxy_fixed.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
