package ndcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testToken = "ndt_abcdefghijkm_" + "Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7Q7"

// fakeConsole answers like the console and keeps every request.
type fakeConsole struct {
	srv  *httptest.Server
	reqs []*http.Request
	body []string
	// answer gives the status and JSON for a request; nil answers 200 {"success":true,"data":[]}
	answer func(r *http.Request) (int, string)
	// redirect is the Location of the answers, "" for none
	redirect string
}

func newFake(t *testing.T) *fakeConsole {
	f := &fakeConsole{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.reqs = append(f.reqs, r)
		f.body = append(f.body, string(b))
		status, body := 200, `{"success":true,"data":[]}`
		if f.answer != nil {
			status, body = f.answer(r)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if f.redirect != "" {
			w.Header().Set("Location", f.redirect)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// runCLI runs the command line with an environment of its own.
func runCLI(t *testing.T, env map[string]string, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	e := &Env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut,
		Getenv:        func(k string) string { return env[k] },
		UserConfigDir: func() (string, error) { return dir, nil },
		Terminal:      func(any) bool { return false },
		Version:       "test",
	}
	code = Main(args, e)
	return code, out.String(), errOut.String()
}

func TestTheTableIsValidAndCoversItsGroups(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Commands) < 400 {
		t.Errorf("only %d commands", len(tab.Commands))
	}
	used := map[string]bool{}
	for _, c := range tab.Commands {
		used[c.Group()] = true
		if strings.Contains(c.Summary+c.Description, "—") || strings.Contains(c.Summary+c.Description, " -- ") || strings.Contains(c.Summary, " · ") {
			t.Errorf("%s: dashes in the summary: %q", c.Name, c.Summary)
		}
		if !strings.HasSuffix(c.Summary, ".") {
			t.Errorf("%s: the summary is no sentence: %q", c.Name, c.Summary)
		}
	}
	for _, g := range tab.Groups {
		if !used[g.Name] {
			t.Errorf("group %s has no command", g.Name)
		}
	}
	// A broken table is refused
	for _, bad := range []string{
		`{"groups":[{"name":"g","summary":"s"}],"commands":[{"name":"g a","summary":"s.","route":"GET /x/{id}"}]}`,
		`{"groups":[{"name":"g","summary":"s"}],"commands":[{"name":"g a","summary":"s.","route":"GET /x","flags":[{"name":"url","in":"query","type":"string"}]}]}`,
		`{"groups":[],"commands":[{"name":"g a","summary":"s.","route":"GET /x"}]}`,
		`{"groups":[{"name":"g","summary":"s"}],"commands":[{"name":"g a","summary":"s.","route":"GET /x"},{"name":"g a","summary":"s.","route":"GET /y"}]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse took %s", bad)
		}
	}
}

func TestACommandBuildsItsRequest(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	code, _, stderr := runCLI(t, env, "", "--json", "devices", "list", "--search", "front desk", "--status", "online", "-q", "x=1")
	if code != 0 {
		t.Fatalf("devices list: %d %s", code, stderr)
	}
	r := f.reqs[0]
	if r.Method != "GET" || r.URL.Path != "/api/devices" || r.URL.Query().Get("search") != "front desk" || r.URL.Query().Get("status") != "online" || r.URL.Query().Get("x") != "1" {
		t.Errorf("request %s %s", r.Method, r.URL)
	}
	if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Cookie") != "" {
		t.Errorf("headers %v", r.Header)
	}
	// Path arguments are escaped, typed flags typed, -F takes JSON
	code, _, stderr = runCLI(t, env, "", "devices", "update", "a/b c", "--note", "Front desk", "-F", "pinned=true", "-f", "user=ann")
	if code != 0 {
		t.Fatalf("devices update: %d %s", code, stderr)
	}
	r = f.reqs[1]
	if r.Method != "PATCH" || r.URL.EscapedPath() != "/api/devices/a%2Fb%20c" {
		t.Errorf("request %s %s", r.Method, r.URL.EscapedPath())
	}
	var body map[string]any
	json.Unmarshal([]byte(f.body[1]), &body)
	if body["note"] != "Front desk" || body["pinned"] != true || body["user"] != "ann" {
		t.Errorf("body %s", f.body[1])
	}
	code, _, _ = runCLI(t, env, "", "devices", "bulk-delete", "--ids", "1,2, 3", "--yes")
	json.Unmarshal([]byte(f.body[2]), &body)
	if ids, _ := body["ids"].([]any); code != 0 || len(ids) != 3 || ids[2] != "3" {
		t.Errorf("list flag: %d %s", code, f.body[2])
	}
}

func TestMistakesAreUsageErrorsAndSendNothing(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	for _, args := range [][]string{
		{"devices", "get"},                        // missing argument
		{"devices", "get", "1", "2"},              // one too many
		{"devices", "list", "--status", "maybe"},  // not in the enum
		{"devices", "list", "--nope", "x"},        // unknown flag
		{"devics", "list"},                        // unknown group
		{"devices", "lst"},                        // unknown action
		{"users", "create", "--username", "ann"},  // required flag missing
		{"devices", "delete", "123"},              // destructive without --yes, not on a terminal
		{"devices", "list", "-F", "x=not json"},   // -F takes JSON
		{"devices", "list", "--timeout", "never"}, // a duration
	} {
		if code, _, stderr := runCLI(t, env, "", args...); code != ExitUsage || stderr == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
	if len(f.reqs) != 0 {
		t.Errorf("%d requests were sent", len(f.reqs))
	}
	if code, _, stderr := runCLI(t, env, "", "devics", "list"); code != ExitUsage || !strings.Contains(stderr, `Did you mean "devices"`) {
		t.Errorf("no suggestion: %s", stderr)
	}
}

func TestExitCodesFollowTheAnswer(t *testing.T) {
	f := newFake(t)
	status := 200
	f.answer = func(*http.Request) (int, string) {
		return status, `{"success":false,"error":"nope","code":"some_code"}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	for s, want := range map[int]int{401: ExitAuth, 403: ExitForbidden, 404: ExitNotFound, 400: ExitRejected, 409: ExitRejected,
		429: ExitRateLimited, 500: ExitServer, 503: ExitServer, 200: ExitFailed} {
		status = s
		code, _, stderr := runCLI(t, env, "", "devices", "get", "1")
		if code != want || !strings.Contains(stderr, "nope") || !strings.Contains(stderr, "some_code") {
			t.Errorf("HTTP %d: exit %d, want %d; %s", s, code, want, stderr)
		}
	}
	// A redirect is not followed: the token could go elsewhere
	redirected := 0
	f.answer = func(r *http.Request) (int, string) {
		if r.URL.Path == "/elsewhere" {
			redirected++
			return 200, `{"success":true,"data":[]}`
		}
		return 302, `{}`
	}
	f.redirect = "/elsewhere"
	if code, _, stderr := runCLI(t, env, "", "devices", "list"); code != ExitFailed || !strings.Contains(stderr, "redirect to /elsewhere") || redirected != 0 {
		t.Errorf("redirect: %d %s, followed %d times", code, stderr, redirected)
	}
	f.redirect = ""
	// Nobody there
	env["NEODESK_URL"] = "http://127.0.0.1:1"
	if code, _, _ := runCLI(t, env, "", "devices", "list"); code != ExitNetwork {
		t.Errorf("no console: %d", code)
	}
}

// The token never shows: not in output, errors, --verbose, config or whoami.
func TestTheTokenIsNeverPrinted(t *testing.T) {
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/current") {
			return 200, `{"success":true,"data":{"api_token":{"id":"abcdefghijkm","name":"ci","hint":"ndt_abcdefghijkm_…","read_only":false,"expires_at":"2027-01-01T00:00:00Z","allow_list":[]},"account":{"username":"ann","role":"admin"},"permissions":["device.view"],"full":false}}`
		}
		return 401, `{"success":false,"error":"The API token is not valid","code":"token_invalid"}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	secret := testToken[len("ndt_abcdefghijkm_"):]
	for _, args := range [][]string{
		{"devices", "list", "--verbose"}, {"whoami"}, {"config"}, {"config", "--json"}, {"devices", "list", "--json"},
		{"api", "GET", "/api/stats?token=" + "x", "--verbose"},
	} {
		_, stdout, stderr := runCLI(t, env, "", args...)
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("%v printed the token:\n%s\n%s", args, stdout, stderr)
		}
	}
}

// A token saved for one console never goes to another one.
func TestASavedTokenStaysWithItsConsole(t *testing.T) {
	saved := newFake(t)
	other := newFake(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := saveConfig(cfg, &Config{URL: saved.srv.URL, Token: testToken}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(cfg)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("config file mode %o", info.Mode().Perm())
		}
	}
	env := map[string]string{"NEODESK_CONFIG": cfg}
	if code, _, stderr := runCLI(t, env, "", "devices", "list"); code != 0 {
		t.Fatalf("with the saved console: %d %s", code, stderr)
	}
	env["NEODESK_URL"] = other.srv.URL
	if code, _, stderr := runCLI(t, env, "", "devices", "list"); code != ExitAuth || !strings.Contains(stderr, "belongs to") {
		t.Errorf("another console: %d %s", code, stderr)
	}
	if code, _, _ := runCLI(t, env, "", "--url", other.srv.URL, "devices", "list"); code != ExitAuth {
		t.Errorf("--url another console: %d", code)
	}
	if len(other.reqs) != 0 {
		t.Errorf("the other console got %d requests", len(other.reqs))
	}
	// With a token of its own the other console is fine
	env["NEODESK_TOKEN"] = testToken
	if code, _, _ := runCLI(t, env, "", "devices", "list"); code != 0 || len(other.reqs) != 1 {
		t.Errorf("other console with its own token: %d, %d requests", code, len(other.reqs))
	}
}

func TestTheConfigFileMustBePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits mean nothing on Windows")
	}
	f := newFake(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	saveConfig(cfg, &Config{URL: f.srv.URL, Token: testToken})
	os.Chmod(cfg, 0o644)
	code, _, stderr := runCLI(t, map[string]string{"NEODESK_CONFIG": cfg}, "", "devices", "list")
	if code != ExitConfig || !strings.Contains(stderr, "chmod 600") || len(f.reqs) != 0 {
		t.Errorf("readable config: %d %s, %d requests", code, stderr, len(f.reqs))
	}
}

func TestPlainHTTPIsForThisMachineOnly(t *testing.T) {
	env := map[string]string{"NEODESK_URL": "http://desk.example.com", "NEODESK_TOKEN": testToken}
	if code, _, stderr := runCLI(t, env, "", "devices", "list"); code != ExitUsage || !strings.Contains(stderr, "plain HTTP") {
		t.Errorf("plain HTTP to another machine: %d %s", code, stderr)
	}
	for _, bad := range []string{"desk.example.com", "ftp://desk", "https://user:pw@desk.example.com", "https://desk/?a=1"} {
		env["NEODESK_URL"] = bad
		if code, _, _ := runCLI(t, env, "", "devices", "list"); code != ExitUsage {
			t.Errorf("%q: exit %d", bad, code)
		}
	}
	env = map[string]string{"NEODESK_URL": "https://desk.example.com", "NEODESK_TOKEN": "not-a-token"}
	if code, _, _ := runCLI(t, env, "", "devices", "list"); code != ExitAuth {
		t.Errorf("a token without ndt_: %d", code)
	}
	if code, _, stderr := runCLI(t, map[string]string{"NEODESK_URL": "https://desk.example.com"}, "", "devices", "list"); code != ExitAuth || !strings.Contains(stderr, "no API token") {
		t.Errorf("no token: %d %s", code, stderr)
	}
}

func TestLoginChecksTheTokenAndSavesIt(t *testing.T) {
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			return 401, `{"success":false,"error":"The API token is not valid","code":"token_invalid"}`
		}
		return 200, `{"success":true,"data":{"api_token":{"id":"abcdefghijkm","name":"ci","hint":"ndt_abcdefghijkm_…"},"account":{"username":"ann","role":"admin"},"permissions":[],"full":true}}`
	}
	cfg := filepath.Join(t.TempDir(), "neodesk", "config.json")
	env := map[string]string{"NEODESK_CONFIG": cfg}
	if code, _, stderr := runCLI(t, env, "", "login", "--url", f.srv.URL, testToken); code != ExitUsage || !strings.Contains(stderr, "history") {
		t.Errorf("token as an argument: %d %s", code, stderr)
	}
	if code, _, _ := runCLI(t, env, "ndt_wrongwrongwr_"+strings.Repeat("A", 40)+"\n", "login", "--url", f.srv.URL); code != ExitAuth {
		t.Errorf("wrong token: %d", code)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("a refused token was saved")
	}
	code, stdout, stderr := runCLI(t, env, testToken+"\n", "login", "--url", f.srv.URL)
	if code != 0 || !strings.Contains(stdout, "Signed in") || strings.Contains(stdout+stderr, testToken[17:]) {
		t.Fatalf("login: %d %s %s", code, stdout, stderr)
	}
	saved, err := loadConfig(cfg)
	if err != nil || saved.Token != testToken || saved.URL != f.srv.URL {
		t.Errorf("saved %+v, %v", saved, err)
	}
	if code, _, _ := runCLI(t, env, "", "logout"); code != 0 {
		t.Errorf("logout: %d", code)
	}
	if saved, _ := loadConfig(cfg); saved.Token != "" || saved.URL != f.srv.URL {
		t.Errorf("after logout %+v", saved)
	}
}

// A server whose answer the command line does not know is told to need
// the release that brought API tokens, 3.6.0-rc.4, not a version that
// never existed (L444).
func TestSignInToAnUnknownServerNamesTheRealRelease(t *testing.T) {
	f := newFake(t)
	f.answer = func(*http.Request) (int, string) { return 200, `<!doctype html><title>Sign in</title>` }
	env := map[string]string{"NEODESK_CONFIG": filepath.Join(t.TempDir(), "config.json")}
	code, _, stderr := runCLI(t, env, testToken+"\n", "login", "--url", f.srv.URL)
	if code == 0 || !strings.Contains(stderr, "3.6.0-rc.4 or later") || strings.Contains(stderr, "3.6.6") {
		t.Errorf("login to a server without API tokens: %d %s", code, stderr)
	}
}

func TestSecretsComeFromStandardInput(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	code, _, stderr := runCLI(t, env, "s3cret-Pass!\n", "users", "create", "--username", "ann", "--role", "operator", "--password-stdin")
	if code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	var body map[string]any
	json.Unmarshal([]byte(f.body[0]), &body)
	if body["password"] != "s3cret-Pass!" || body["username"] != "ann" || body["role"] != "operator" {
		t.Errorf("body %s", f.body[0])
	}
}

func TestAnswersAreShownForPeople(t *testing.T) {
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		switch {
		case r.URL.Path == "/api/devices":
			return 200, `{"success":true,"data":{"devices":[{"id":"111","hostname":"front","online":true,"os":"Windows","note":"line\nbreak"},{"id":"222","hostname":"back","online":false}],"total":2}}`
		case r.URL.Path == "/api/stats":
			return 200, `{"success":true,"data":{"devices":{"online":1,"total":2},"version":"3.6"}}`
		}
		return 200, `{"success":true}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	_, out, _ := runCLI(t, env, "", "devices", "list")
	if !strings.Contains(out, "ID") || !strings.Contains(out, "front") || !strings.Contains(out, "line break") || !strings.Contains(out, "(2 rows)") {
		t.Errorf("table:\n%s", out)
	}
	_, out, _ = runCLI(t, env, "", "dashboard", "stats")
	if !strings.Contains(out, "devices.online: 1") || !strings.Contains(out, "version: 3.6") {
		t.Errorf("object:\n%s", out)
	}
	_, out, _ = runCLI(t, env, "", "devices", "unban", "1")
	if strings.TrimSpace(out) != "Done." {
		t.Errorf("write: %q", out)
	}
	_, out, _ = runCLI(t, env, "", "--json", "dashboard", "stats")
	var v map[string]any
	if json.Unmarshal([]byte(out), &v) != nil || v["success"] != true {
		t.Errorf("--json: %s", out)
	}
	path := filepath.Join(t.TempDir(), "stats.json")
	if code, _, _ := runCLI(t, env, "", "dashboard", "stats", "--output", path); code != 0 {
		t.Errorf("--output: %d", code)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"version":"3.6"`) {
		t.Errorf("--output wrote %q", b)
	}
}

func TestHelpOnEveryLevel(t *testing.T) {
	tab, _ := Load()
	if code, out, _ := runCLI(t, nil, "", "--help"); code != 0 || !strings.Contains(out, "Exit codes") || !strings.Contains(out, "devices") {
		t.Errorf("root help: %d", code)
	}
	if code, out, _ := runCLI(t, nil, ""); code != ExitUsage || !strings.Contains(out, "Usage") {
		t.Errorf("no arguments: %d", code)
	}
	for _, g := range tab.Groups {
		if code, out, _ := runCLI(t, nil, "", g.Name, "--help"); code != 0 || !strings.Contains(out, "Actions:") {
			t.Errorf("%s --help: %d", g.Name, code)
		}
	}
	for _, c := range tab.Commands {
		code, out, _ := runCLI(t, nil, "", append(strings.Fields(c.Name), "--help")...)
		if code != 0 || !strings.Contains(out, "Usage:") || !strings.Contains(out, c.Route) {
			t.Errorf("%s --help: %d", c.Name, code)
		}
	}
	for _, m := range metaNames() {
		if code, out, _ := runCLI(t, nil, "", "help", m); code != 0 || !strings.Contains(out, "Usage") {
			t.Errorf("help %s: %d %q", m, code, out)
		}
	}
	if code, out, _ := runCLI(t, nil, "", "help", "devices", "list"); code != 0 || !strings.Contains(out, "--search") {
		t.Errorf("help devices list: %d", code)
	}
}

func TestCompletionKnowsEveryLevel(t *testing.T) {
	tab, _ := Load()
	has := func(list []string, s string) bool {
		for _, v := range list {
			if v == s {
				return true
			}
		}
		return false
	}
	if c := completions(tab, []string{"dev"}); !has(c, "devices") || !has(c, "device-files") || has(c, "users") {
		t.Errorf("groups: %v", c)
	}
	if c := completions(tab, []string{"devices", "ba"}); !has(c, "ban") || len(c) != 1 {
		t.Errorf("actions: %v", c)
	}
	if c := completions(tab, []string{"devices", "list", "--st"}); !has(c, "--status") {
		t.Errorf("flags: %v", c)
	}
	if c := completions(tab, []string{"devices", "list", "--status", ""}); !has(c, "online") || !has(c, "offline") {
		t.Errorf("enum values: %v", c)
	}
	if c := completions(tab, []string{"users", "create", "--pass"}); !has(c, "--password-stdin") {
		t.Errorf("secret flags: %v", c)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if code, out, _ := runCLI(t, nil, "", "completion", shell); code != 0 || !strings.Contains(out, "__complete") {
			t.Errorf("completion %s: %d", shell, code)
		}
	}
}

func TestLocalRunsTheServerCommands(t *testing.T) {
	var got []string
	var out, errOut bytes.Buffer
	e := &Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" },
		UserConfigDir: func() (string, error) { return t.TempDir(), nil }, Terminal: func(any) bool { return false },
		Local: map[string]LocalCommand{"backup": {Usage: "backup ...", Run: func(args []string, _ io.Reader, _, _ io.Writer) int {
			got = args
			return 7
		}}}}
	if code := Main([]string{"local", "backup", "--out", "x"}, e); code != 7 || strings.Join(got, " ") != "--out x" {
		t.Errorf("local backup: %d %v", code, got)
	}
	if code := Main([]string{"local", "nope"}, e); code != ExitUsage {
		t.Errorf("unknown local command: %d", code)
	}
}

// Server text never steers the terminal: control characters in a device
// name or an error message are replaced, in tables, lines and JSON.
func TestControlCharactersNeverReachTheTerminal(t *testing.T) {
	f := newFake(t)
	evil := `evil\u001b]0;owned\u0007\u001b[2J\u009b31m\u007fname`
	// JSON may also carry DEL and the C1 controls raw
	raw := "evil\u009b31m\u007fname"
	f.answer = func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/api/devices":
			return 200, `{"success":true,"data":{"devices":[{"id":"1","hostname":"` + evil + `"}]}}`
		case "/api/stats":
			return 200, `{"success":true,"data":{"note":"` + evil + `","raw":"` + raw + `","` + evil + `":1}}`
		}
		return 403, `{"success":false,"error":"` + evil + `","code":"x\u001b[1m"}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	clean := func(what, s string) {
		t.Helper()
		if strings.ContainsAny(s, "\x1b\x07\u009b\x7f") {
			t.Errorf("%s holds control characters: %q", what, s)
		}
		if !strings.Contains(s, "evil") {
			t.Errorf("%s lost the text: %q", what, s)
		}
	}
	_, out, _ := runCLI(t, env, "", "devices", "list")
	clean("table", out)
	_, out, _ = runCLI(t, env, "", "dashboard", "stats")
	clean("lines", out)
	_, out, _ = runCLI(t, env, "", "--json", "dashboard", "stats")
	clean("JSON", out)
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("--json is no longer JSON: %v", err)
	} else if data, _ := v["data"].(map[string]any); data["raw"] != raw || !strings.Contains(fmt.Sprint(data["note"]), "\u009b") {
		t.Errorf("--json changed the values: %q %q", data["raw"], data["note"])
	}
	_, _, stderr := runCLI(t, env, "", "devices", "get", "1")
	clean("error", stderr)
}

// "." and ".." are no arguments: a proxy that cleans paths would send
// the request to another route.
func TestDotArgumentsAreRefused(t *testing.T) {
	f := newFake(t)
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	for _, bad := range []string{".", "..", "x/../y"} {
		if code, _, stderr := runCLI(t, env, "", "devices", "get", bad); code != ExitUsage || !strings.Contains(stderr, "cannot be") {
			t.Errorf("%q: %d %s", bad, code, stderr)
		}
	}
	if len(f.reqs) != 0 {
		t.Errorf("%d requests sent", len(f.reqs))
	}
	if code, _, _ := runCLI(t, env, "", "devices", "get", "a.b"); code != 0 || len(f.reqs) != 1 || f.reqs[0].URL.Path != "/api/devices/a.b" {
		t.Errorf("a dot inside an ID: %d", code)
	}
}

// A token from the environment or a flag goes only to an address from the
// environment or a flag: a config file (perhaps one someone else wrote)
// cannot send it elsewhere.
func TestATokenFromTheEnvironmentNeedsItsAddressToo(t *testing.T) {
	f := newFake(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfig(cfg, &Config{URL: f.srv.URL}); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "token")
	os.WriteFile(tokenFile, []byte(testToken), 0o600)
	env := map[string]string{"NEODESK_CONFIG": cfg, "NEODESK_TOKEN": testToken}
	if code, _, stderr := runCLI(t, env, "", "devices", "list"); code != ExitUsage || !strings.Contains(stderr, "NEODESK_URL") {
		t.Errorf("NEODESK_TOKEN with the config's address: %d %s", code, stderr)
	}
	if code, _, _ := runCLI(t, map[string]string{"NEODESK_CONFIG": cfg}, "", "--token-file", tokenFile, "devices", "list"); code != ExitUsage {
		t.Errorf("--token-file with the config's address: %d", code)
	}
	if len(f.reqs) != 0 {
		t.Errorf("the config's console got %d requests", len(f.reqs))
	}
	env["NEODESK_URL"] = f.srv.URL
	if code, _, stderr := runCLI(t, env, "", "devices", "list"); code != 0 {
		t.Errorf("both from the environment: %d %s", code, stderr)
	}
}

// --insecure is never saved, and a connection that is not checked goes
// past any proxy.
func TestInsecureIsNotSavedAndSkipsTheProxy(t *testing.T) {
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		return 200, `{"success":true,"data":{"api_token":{"id":"abcdefghijkm","name":"ci","hint":"ndt_abcdefghijkm_…"},"account":{"username":"ann","role":"admin"},"permissions":[],"full":true}}`
	}
	cfg := filepath.Join(t.TempDir(), "config.json")
	code, _, stderr := runCLI(t, map[string]string{"NEODESK_CONFIG": cfg}, testToken+"\n", "login", "--url", f.srv.URL, "--insecure")
	if code != 0 || !strings.Contains(stderr, "not saved") {
		t.Fatalf("login --insecure: %d %s", code, stderr)
	}
	if b, _ := os.ReadFile(cfg); strings.Contains(string(b), "insecure") {
		t.Errorf("--insecure was saved: %s", b)
	}
	env := &Env{}
	for _, c := range []struct {
		direct bool
		proxy  bool
	}{{true, false}, {false, true}} {
		client, err := httpClient(env, false, c.direct, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		tr, _ := client.Transport.(*http.Transport)
		if tr == nil || (tr.Proxy != nil) != c.proxy {
			t.Errorf("direct %v: proxy set %v", c.direct, tr != nil && tr.Proxy != nil)
		}
	}
}

// The config file the command line writes passes its own check, on every
// system (on Windows the owner check).
func TestTheSavedConfigPassesTheCheck(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfig(cfg, &Config{URL: "https://desk.example.com", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	if c, err := loadConfig(cfg); err != nil || c.Token != testToken {
		t.Errorf("own config: %+v %v", c, err)
	}
	// A file of another account (and open to everyone) is refused
	other := "/etc/passwd"
	if runtime.GOOS == "windows" {
		other = filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	}
	if _, err := loadConfig(other); !errors.Is(err, ErrConfigOpen) {
		t.Errorf("%s as the config: %v", other, err)
	}
}

// L431: neodesk --help says where the installer's menu and options went:
// neodesk-setup, which sudo neodesk and the old options still reach.
func TestTheHelpNamesTheInstallersManager(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {}} {
		_, out, _ := runCLI(t, nil, "", args...)
		if !strings.Contains(out, "neodesk-setup") || !strings.Contains(out, "sudo neodesk") || !strings.Contains(out, "--update") {
			t.Errorf("neodesk %v does not name neodesk-setup:\n%s", args, out)
		}
	}
}

// What the agent test found: the secret flags' -stdin form is in the
// flags list and the usage, every command of the command line itself
// takes --help, a record with an empty list is no empty table, and a
// true-only query flag is a switch.
func TestHelpAndOutputFromTheAgentTest(t *testing.T) {
	_, out, _ := runCLI(t, nil, "", "users", "create", "--help")
	if !strings.Contains(out, "(--password-stdin | --password PASSWORD)") || !strings.Contains(out, "\n  --password-stdin ") || !strings.Contains(out, "8 to 128 characters") {
		t.Errorf("users create --help:\n%s", out)
	}
	for _, meta := range []string{"version", "whoami", "config", "login", "mcp", "describe", "completion"} {
		code, out, _ := runCLI(t, nil, "", meta, "--help")
		if code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("%s --help: %d %q", meta, code, out)
		}
	}
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		if r.Method == "DELETE" {
			return 200, `{"success":true,"data":{"id":"abcdefghijkm","name":"old-ci","allow_list":[],"state":"revoked"}}`
		}
		return 200, `{"success":true,"data":{"devices":[],"total":0}}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	_, out, _ = runCLI(t, env, "", "api-tokens", "revoke", "abcdefghijkm", "--yes")
	if strings.Contains(out, "No results.") || !strings.Contains(out, "state: revoked") {
		t.Errorf("a record with an empty list:\n%s", out)
	}
	if code, out, stderr := runCLI(t, env, "", "devices", "list", "--include-deleted"); code != 0 || !strings.Contains(out, "No results.") || f.reqs[len(f.reqs)-1].URL.Query().Get("includeDeleted") != "true" {
		t.Errorf("--include-deleted: %d %s %s", code, out, stderr)
	}
}

// The server writes most times as "2006-01-02 15:04:05" in UTC, without a
// zone: tables and records say UTC after them, so nobody reads them as
// local time. Times with a zone and other text stay as they are (L431).
func TestTimesWithoutAZoneSayUTC(t *testing.T) {
	f := newFake(t)
	f.answer = func(r *http.Request) (int, string) {
		if r.URL.Path == "/api/devices" {
			return 200, `{"success":true,"data":{"devices":[{"id":"111","hostname":"front","last_online":"2026-11-24 10:55:31"},{"id":"222","hostname":"back","last_online":"2026-11-24T09:00:00.000Z"}],"total":2}}`
		}
		return 200, `{"success":true,"data":{"created_at":"2026-11-23 08:01:02","note":"2026-11-23 08:01:02 was a Monday"}}`
	}
	env := map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}
	_, out, _ := runCLI(t, env, "", "devices", "list")
	if !strings.Contains(out, "2026-11-24 10:55:31 UTC") || !strings.Contains(out, "2026-11-24T09:00:00.000Z") || strings.Contains(out, "000Z UTC") {
		t.Errorf("table:\n%s", out)
	}
	_, out, _ = runCLI(t, env, "", "dashboard", "stats")
	if !strings.Contains(out, "created_at: 2026-11-23 08:01:02 UTC") || !strings.Contains(out, "note: 2026-11-23 08:01:02 was a Monday") {
		t.Errorf("record:\n%s", out)
	}
	_, out, _ = runCLI(t, env, "", "--json", "dashboard", "stats")
	if strings.Contains(out, "UTC") {
		t.Errorf("--json changed a time: %s", out)
	}
}
