package cdap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/audit"
	"github.com/unitronix/betterdesk-server/auth"
)

// The SDKs in sdks/ and the reference bridges in bridges/ run here against
// a gateway of this package the way their users start them: each SDK's
// example, and each bridge with its example config (BUGS L217). A proxy
// between them and the gateway records what each side sends, so a test
// checks both what the gateway kept and that it answered nothing with an
// error.
//
// The tests need Node.js with the Node SDK built (npm ci and npm run build
// in sdks/nodejs) and python3 with websockets, pymodbus for the Modbus
// bridge and aiohttp for the REST bridge. Without them they skip;
// CDAP_SDK_TESTS=1 makes a missing tool a failure.

const sdkTestKey = "apikey-sdk-0123456789abcdef"

// sdkWait is how long a test waits for something an SDK does.
const sdkWait = 30 * time.Second

// sdkRepoRoot is the repository's root, two folders up.
func sdkRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sdks")); err != nil {
		t.Skip("no sdks folder next to betterdesk-server")
	}
	return root
}

// needTool skips the test when a tool is missing, or fails it with
// CDAP_SDK_TESTS=1.
func needTool(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv("CDAP_SDK_TESTS") == "1" {
		t.Fatal(err)
	}
	t.Skip(err)
}

// nodeSDK is the Node SDK's folder, built.
func nodeSDK(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(sdkRepoRoot(t), "sdks", "nodejs")
	_, err := exec.LookPath("node")
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(dir, "dist", "index.js")); statErr != nil {
			err = fmt.Errorf("the Node SDK is not built (npm ci && npm run build in sdks/nodejs): %w", statErr)
		}
	}
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(dir, "node_modules", "ws")); statErr != nil {
			err = fmt.Errorf("the Node SDK has no node_modules (npm ci in sdks/nodejs): %w", statErr)
		}
	}
	needTool(t, err)
	return dir
}

// python is python3, with the modules given importable.
func python(t *testing.T, modules ...string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err == nil {
		if out, runErr := exec.Command(py, "-c", "import "+strings.Join(modules, ", ")).CombinedOutput(); runErr != nil {
			err = fmt.Errorf("python3 cannot import %s: %s", strings.Join(modules, ", "), out)
		}
	}
	needTool(t, err)
	return py
}

// pythonPath puts dir in front of PYTHONPATH.
func pythonPath(dir string) string {
	if p := os.Getenv("PYTHONPATH"); p != "" {
		return "PYTHONPATH=" + dir + string(os.PathListSeparator) + p
	}
	return "PYTHONPATH=" + dir
}

// sdkRun is a gateway, the proxy in front of it and the SDK process of one
// test.
type sdkRun struct {
	t     *testing.T
	w     *cdapWorld
	audit *audit.Logger
	proxy *recordingProxy
	proc  *sdkProcess
}

func newSDKRun(t *testing.T) *sdkRun {
	t.Helper()
	w := newCDAPWorld(t)
	al := audit.NewLogger("")
	w.g.SetAuditLogger(al)
	createAPIKey(t, w.db, sdkTestKey, "sdk", auth.RoleGlobalAdmin, "")
	return &sdkRun{t: t, w: w, audit: al, proxy: newRecordingProxy(t, w.url)}
}

// env is what the SDK examples read: the gateway (through the proxy), the
// key and the device id.
func (r *sdkRun) env(deviceID string) []string {
	return []string{
		"CDAP_SERVER=" + r.proxy.url,
		"CDAP_API_KEY=" + sdkTestKey,
		"CDAP_DEVICE_ID=" + deviceID,
	}
}

// sdkProcess is a running SDK program and what it printed.
type sdkProcess struct {
	mu   sync.Mutex
	out  bytes.Buffer
	done chan struct{}
}

func (p *sdkProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(b)
}

func (p *sdkProcess) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// start runs a program in dir until the test ends.
func (r *sdkRun) start(dir string, env []string, name string, args ...string) {
	r.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	p := &sdkProcess{done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p, p
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	r.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-p.done
	})
	r.proc = p
}

// waitFor waits until cond holds, failing the test when the program ends
// first or after sdkWait.
func (r *sdkRun) waitFor(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(sdkWait)
	for !cond() {
		select {
		case <-r.proc.done:
			r.t.Fatalf("%s: the program ended first%s", what, r.report())
		default:
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("%s: not within %s%s", what, sdkWait, r.report())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// report is what the program printed and what the proxy saw, for a
// failure.
func (r *sdkRun) report() string {
	return fmt.Sprintf("\nsent by the device: %v\nerrors from the gateway: %v\noutput:\n%s",
		r.proxy.deviceTypes(), r.proxy.errors(), r.proc.output())
}

// registered waits for the device and returns its manifest.
func (r *sdkRun) registered(deviceID string) *Manifest {
	r.t.Helper()
	r.waitFor("registration of "+deviceID, func() bool { return r.w.g.IsConnected(deviceID) })
	return r.w.g.GetDeviceConn(deviceID).Manifest
}

// value is a widget's value as the console reads it (GET .../state).
func (r *sdkRun) value(deviceID, widgetID string) (any, bool) {
	state, ok := r.w.g.GetDeviceWidgetState(deviceID)
	if !ok {
		return nil, false
	}
	v, ok := state[widgetID]
	return v, ok
}

// hasValue waits until a widget has the value want.
func (r *sdkRun) hasValue(deviceID, widgetID string, want any) {
	r.t.Helper()
	r.waitFor(fmt.Sprintf("%s = %v", widgetID, want), func() bool {
		v, ok := r.value(deviceID, widgetID)
		return ok && v == want
	})
}

// firing is a firing alert as the console reads it (GET /api/cdap/alerts),
// or nil.
func (r *sdkRun) firing(deviceID, alertID string) *AlertState {
	for _, a := range r.w.g.GetActiveAlerts(deviceID) {
		if a.AlertID == alertID {
			return a
		}
	}
	return nil
}

// command sends a command as the console's command route does and returns
// its id.
func (r *sdkRun) command(deviceID, widgetID, action string, value any) string {
	r.t.Helper()
	id := fmt.Sprintf("cmd_%s_%s_%d", deviceID, widgetID, time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.w.g.SendCommandJSON(ctx, deviceID, id, widgetID, action, value, "tester", ""); err != nil {
		r.t.Fatalf("command %s %s: %v", widgetID, action, err)
	}
	return id
}

// answered waits for the device's answer to a command in the server's
// events (the audit log the console shows) and fails unless it is ok.
func (r *sdkRun) answered(commandID string) {
	r.t.Helper()
	status := ""
	r.waitFor("the answer to "+commandID, func() bool {
		for _, e := range r.audit.RecentByAction("cdap_command_response", 100) {
			if e.Details["command_id"] == commandID {
				status = e.Details["status"]
				return true
			}
		}
		return false
	})
	if status != "ok" {
		r.t.Fatalf("the answer to %s has status %q, want ok%s", commandID, status, r.report())
	}
}

// sent fails unless the device sent each message type.
func (r *sdkRun) sent(types ...string) {
	r.t.Helper()
	for _, typ := range types {
		if !r.proxy.sentType(typ) {
			r.t.Errorf("the device sent no %s%s", typ, r.report())
		}
	}
}

// noErrors fails when the gateway answered anything with an error.
func (r *sdkRun) noErrors() {
	r.t.Helper()
	if errs := r.proxy.errors(); len(errs) > 0 {
		r.t.Errorf("the gateway answered with errors%s", r.report())
	}
}

// widgetsAre fails unless the manifest has exactly these widget ids.
func widgetsAre(t *testing.T, m *Manifest, ids ...string) {
	t.Helper()
	var got []string
	for _, w := range m.Widgets {
		got = append(got, w.ID)
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatalf("widgets %v, want %v", got, ids)
	}
}

// recordingProxy passes a device's WebSocket on to the gateway and records
// the type of each message the device sends and each message the gateway
// sends. It asks the gateway for the subprotocols the device asked for and
// hands the device the one the gateway chose, so a handshake fails here
// as it fails at the gateway.
type recordingProxy struct {
	url string

	mu         sync.Mutex
	fromDevice []string
	fromServer []Message
}

func newRecordingProxy(t *testing.T, gatewayURL string) *recordingProxy {
	t.Helper()
	p := &recordingProxy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var asked []string
		for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
			for _, s := range strings.Split(h, ",") {
				if s = strings.TrimSpace(s); s != "" {
					asked = append(asked, s)
				}
			}
		}
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
		up, _, err := websocket.Dial(dialCtx, gatewayURL, &websocket.DialOptions{Subprotocols: asked})
		dialCancel()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer up.CloseNow()
		var chosen []string
		if s := up.Subprotocol(); s != "" {
			chosen = []string{s}
		}
		down, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: chosen})
		if err != nil {
			return
		}
		defer down.CloseNow()
		up.SetReadLimit(16 << 20)
		down.SetReadLimit(16 << 20)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			p.pipe(ctx, down, up, true)
			cancel()
		}()
		p.pipe(ctx, up, down, false)
	}))
	t.Cleanup(srv.Close)
	p.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/cdap"
	return p
}

// pipe copies messages from one side to the other until either ends, and
// ends the other side with the same close code.
func (p *recordingProxy) pipe(ctx context.Context, from, to *websocket.Conn, fromDevice bool) {
	for {
		typ, data, err := from.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) && ce.Code != websocket.StatusNoStatusRcvd {
				_ = to.Close(ce.Code, ce.Reason)
			} else {
				_ = to.CloseNow()
			}
			return
		}
		if typ == websocket.MessageText {
			p.record(data, fromDevice)
		}
		if err := to.Write(ctx, typ, data); err != nil {
			return
		}
	}
}

func (p *recordingProxy) record(data []byte, fromDevice bool) {
	var m Message
	if json.Unmarshal(data, &m) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if fromDevice {
		p.fromDevice = append(p.fromDevice, m.Type)
	} else {
		p.fromServer = append(p.fromServer, m)
	}
}

// deviceTypes are the types of the device's messages, in order, each once.
func (p *recordingProxy) deviceTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	var types []string
	for _, typ := range p.fromDevice {
		if !seen[typ] {
			seen[typ] = true
			types = append(types, typ)
		}
	}
	return types
}

func (p *recordingProxy) sentType(typ string) bool {
	for _, t := range p.deviceTypes() {
		if t == typ {
			return true
		}
	}
	return false
}

// errors are the payloads of the gateway's error messages.
func (p *recordingProxy) errors() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []string
	for _, m := range p.fromServer {
		if m.Type == "error" {
			errs = append(errs, string(m.Payload))
		}
	}
	return errs
}

// checkGreenhouse drives the SDK examples' greenhouse: it registers, sends
// its values, raises the frost alert, switches the heater on a command and
// the alert clears as it warms up.
func (r *sdkRun) checkGreenhouse(deviceID string) {
	t := r.t
	t.Helper()
	m := r.registered(deviceID)
	if m.Device.Name != "Greenhouse" || m.Device.Type != "iot" {
		t.Fatalf("device %+v, want the Greenhouse of type iot", m.Device)
	}
	widgetsAre(t, m, "temp", "humidity", "heater", "status")
	if len(m.Alerts) != 1 || m.Alerts[0].ID != "frost" || m.Alerts[0].Severity != "warning" {
		t.Fatalf("alerts %+v, want frost", m.Alerts)
	}

	r.waitFor("temperature and humidity in a bulk update", func() bool {
		_, temp := r.value(deviceID, "temp")
		_, humidity := r.value(deviceID, "humidity")
		return temp && humidity && r.proxy.sentType("bulk_update")
	})
	r.waitFor("the frost alert under Active alerts", func() bool { return r.firing(deviceID, "frost") != nil })
	if a := r.firing(deviceID, "frost"); a == nil || !strings.HasPrefix(a.Message, "Temperature is ") || a.Label != "Frost" {
		t.Fatalf("frost alert %+v", a)
	}

	r.answered(r.command(deviceID, "heater", "set", true))
	r.hasValue(deviceID, "heater", true)
	r.hasValue(deviceID, "status", "Heating")
	r.waitFor("the frost alert clears", func() bool { return r.firing(deviceID, "frost") == nil })

	r.sent("auth", "register", "bulk_update", "state_update", "command_response", "log")
	r.noErrors()
}

// TestNodeSDKExample runs the Node SDK's example against the gateway.
func TestNodeSDKExample(t *testing.T) {
	dir := nodeSDK(t)
	r := newSDKRun(t)
	r.start(dir, r.env("greenhouse-node"), "node", filepath.Join("examples", "greenhouse.mts"))
	r.checkGreenhouse("greenhouse-node")
}

// TestPythonSDKExample runs the Python SDK's example against the gateway.
func TestPythonSDKExample(t *testing.T) {
	py := python(t, "websockets")
	dir := filepath.Join(sdkRepoRoot(t), "sdks", "python")
	r := newSDKRun(t)
	r.start(dir, append(r.env("greenhouse-python"), pythonPath(dir)), py, filepath.Join("examples", "greenhouse.py"))
	r.checkGreenhouse("greenhouse-python")
}

// startBridge runs a bridge with its example config, pointed at the
// gateway with the test's key and changed by edit, and its target stood
// in for by testdata/sdk/bridge_harness.py. It returns the config's device
// id.
func (r *sdkRun) startBridge(py, kind, script string, edit func(cfg map[string]any)) string {
	r.t.Helper()
	root := sdkRepoRoot(r.t)
	dir := filepath.Join(root, "bridges", kind)
	raw, err := os.ReadFile(filepath.Join(dir, "config.example.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		r.t.Fatal(err)
	}
	cdapCfg, _ := cfg["cdap"].(map[string]any)
	if cdapCfg == nil {
		r.t.Fatal("the example config has no cdap block")
	}
	cdapCfg["server"] = r.proxy.url
	cdapCfg["api_key"] = sdkTestKey
	if edit != nil {
		edit(cfg)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(r.t.TempDir(), "config.json")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		r.t.Fatal(err)
	}
	harness, err := filepath.Abs(filepath.Join("testdata", "sdk", "bridge_harness.py"))
	if err != nil {
		r.t.Fatal(err)
	}
	r.start(dir, []string{pythonPath(filepath.Join(root, "sdks", "python"))},
		py, harness, kind, filepath.Join(dir, script), path)
	id, _ := cdapCfg["device_id"].(string)
	return id
}

// freePort is a TCP port on 127.0.0.1 that nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestModbusBridge runs the Modbus bridge with its example config against
// a Modbus TCP server (pymodbus) on a free port.
func TestModbusBridge(t *testing.T) {
	py := python(t, "websockets", "pymodbus")
	port := freePort(t)
	r := newSDKRun(t)
	id := r.startBridge(py, "modbus", "bridge_modbus.py", func(cfg map[string]any) {
		modbus, _ := cfg["modbus"].(map[string]any)
		if modbus == nil {
			t.Fatal("the example config has no modbus block")
		}
		modbus["host"], modbus["port"] = "127.0.0.1", port
	})
	m := r.registered(id)
	if m.Device.Type != "scada" || m.Device.Name != "Modbus Bridge" || m.Bridge.Name != "modbus" {
		t.Fatalf("device %+v bridge %+v", m.Device, m.Bridge)
	}
	widgetsAre(t, m, "temperature", "humidity", "relay_1", "setpoint")

	r.hasValue(id, "temperature", 21.5)
	r.hasValue(id, "humidity", 45.5)
	r.hasValue(id, "relay_1", false)
	r.hasValue(id, "setpoint", 20.0)
	r.waitFor("a bulk update", func() bool { return r.proxy.sentType("bulk_update") })

	r.answered(r.command(id, "relay_1", "set", true))
	r.hasValue(id, "relay_1", true)
	r.answered(r.command(id, "setpoint", "set", 22.5))

	r.sent("auth", "register", "bulk_update", "command_response")
	r.noErrors()
}

// TestSNMPBridge runs the SNMP bridge with its example config.
func TestSNMPBridge(t *testing.T) {
	py := python(t, "websockets")
	r := newSDKRun(t)
	id := r.startBridge(py, "snmp", "bridge_snmp.py", nil)
	m := r.registered(id)
	if m.Device.Type != "network" || m.Device.Name != "SNMP Bridge" || m.Bridge.Name != "snmp" {
		t.Fatalf("device %+v bridge %+v", m.Device, m.Bridge)
	}
	widgetsAre(t, m, "sys_name", "sys_uptime", "cpu_load", "mem_total", "mem_avail", "if_in_octets")

	r.hasValue(id, "sys_name", "core-switch")
	r.hasValue(id, "sys_uptime", "1d 00:00:00")
	r.hasValue(id, "cpu_load", 25.0)
	r.hasValue(id, "mem_total", "8.0 GB")
	r.hasValue(id, "mem_avail", "2.0 GB")
	r.waitFor("a bulk update", func() bool { return r.proxy.sentType("bulk_update") })

	r.sent("auth", "register", "bulk_update")
	r.noErrors()
}

// TestRESTBridge runs the REST and webhook bridge with its example config,
// its webhook on a free port.
func TestRESTBridge(t *testing.T) {
	py := python(t, "websockets", "aiohttp")
	port := freePort(t)

	r := newSDKRun(t)
	id := r.startBridge(py, "rest-webhook", "bridge_rest.py", func(cfg map[string]any) {
		httpCfg, _ := cfg["http"].(map[string]any)
		if httpCfg == nil {
			t.Fatal("the example config has no http block")
		}
		httpCfg["webhook_port"] = port
	})
	m := r.registered(id)
	if m.Device.Type != "iot" || m.Device.Name != "REST Bridge" || m.Bridge.Name != "rest" {
		t.Fatalf("device %+v bridge %+v", m.Device, m.Bridge)
	}
	widgetsAre(t, m, "weather_temp", "ha_light", "doorbell")

	r.hasValue(id, "weather_temp", -3.5)
	r.hasValue(id, "ha_light", true)

	r.answered(r.command(id, "ha_light", "set", false))
	r.hasValue(id, "ha_light", false)

	url := fmt.Sprintf("http://127.0.0.1:%d/webhook", port)
	r.waitFor("the webhook", func() bool {
		resp, err := http.Post(url, "application/json", strings.NewReader(`{"event":"ring","timestamp":"2026-11-22T10:00:00Z"}`))
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	r.hasValue(id, "doorbell", "2026-11-22T10:00:00Z")
	r.waitFor("the doorbell alert under Active alerts", func() bool { return r.firing(id, "wh_doorbell") != nil })
	if a := r.firing(id, "wh_doorbell"); a.Severity != "info" || a.Message != "Doorbell rang" {
		t.Fatalf("doorbell alert %+v", a)
	}
	r.waitFor("a bulk update", func() bool { return r.proxy.sentType("bulk_update") })

	r.sent("auth", "register", "bulk_update", "state_update", "command_response")
	r.noErrors()
}
