package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`

// shell is a fake shell: what each command line prints, and a note of
// every command line with its time limit.
type shell struct {
	outputs map[string]string
	ran     []string
}

func (s *shell) run(_ context.Context, command string, timeout time.Duration) (string, error) {
	s.ran = append(s.ran, timeout.String()+" "+command)
	out, ok := s.outputs[command]
	if !ok {
		return "", errors.New("exit status 127")
	}
	return out, nil
}

func noFiles(string) ([]byte, error) { return nil, fs.ErrNotExist }

// opener opens the files read finds, in memory.
func opener(read func(string) ([]byte, error)) func(string) (io.ReadSeekCloser, error) {
	return func(path string) (io.ReadSeekCloser, error) {
		content, err := read(path)
		if err != nil {
			return nil, err
		}
		n := 0
		return countingFile{bytes.NewReader(content), &n}, nil
	}
}

func fixedProcess() ProcessInfo {
	return ProcessInfo{PID: 4711, Uptime: 90, HeapUsed: 12, HeapTotal: 20, RSS: 64}
}

func linuxSite(t *testing.T, sh *shell, read func(string) ([]byte, error)) *site {
	if read == nil {
		read = noFiles
	}
	return newSite(t, Options{Run: sh.run, Platform: "linux", TotalMemory: func() uint64 { return 8 << 30 }, OpenFile: opener(read), Process: fixedProcess})
}

func TestRoutesInTheOrderOfTheOldFile(t *testing.T) {
	s := newSite(t, Options{})
	var got []string
	for _, route := range s.d.Router.Routes() {
		if strings.HasPrefix(route.Path, "/api/") {
			got = append(got, route.Method+" "+route.Path)
		}
	}
	want := "GET /api/system/info\nGET /api/logs/recent\nGET /api/database/stats\nGET /api/docker/containers\nPOST /api/system/exec\nGET /api/speed-test"
	if strings.Join(got, "\n") != want {
		t.Errorf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func TestGuards(t *testing.T) {
	sh := &shell{outputs: map[string]string{"whoami": "root\n"}}
	s := linuxSite(t, sh, nil)
	reads := []string{"/api/system/info", "/api/logs/recent", "/api/database/stats", "/api/docker/containers", "/api/speed-test?size=1"}
	for _, path := range reads {
		if rec := s.call("", "GET", path, ""); rec.Code != 401 || rec.Body.String() != signedOut {
			t.Errorf("%s signed out: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if status, answer := s.post("", "/api/system/exec", `{"command":"whoami"}`); status != 401 || answer != signedOut {
		t.Errorf("exec signed out: %d %s", status, answer)
	}
	// guest_issuer may not see metrics, a viewer may not run commands
	guest, viewer := s.signIn("guest_issuer"), s.signIn("viewer")
	for _, path := range reads {
		want := `{"success":false,"error":"Permission denied: metrics.view"}`
		if hostDetails[path] {
			want = `{"success":false,"error":"Permission denied: server.config"}`
		}
		if rec := s.call(guest, "GET", path, ""); rec.Code != 403 || rec.Body.String() != want {
			t.Errorf("%s as guest_issuer: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if status, answer := s.post(viewer, "/api/system/exec", `{"command":"whoami"}`); status != 403 || answer != `{"success":false,"error":"Permission denied: server.config"}` {
		t.Errorf("exec as viewer: %d %s", status, answer)
	}
	if len(sh.ran) != 0 {
		t.Errorf("refused requests ran %q", sh.ran)
	}
	if rec := s.call(viewer, "GET", "/api/speed-test?size=1", ""); rec.Code != 200 {
		t.Errorf("speed test as viewer: %d", rec.Code)
	}
}

// hostDetails are the reads that show what other services of the host
// run with, for server.config alone (BUGS RV47).
var hostDetails = map[string]bool{"/api/logs/recent": true, "/api/database/stats": true, "/api/docker/containers": true}

// TestHostDetailsNeedServerConfig: a viewer and an operator, who have
// metrics.view, get the disks and the console's own process but no process
// list, and no logs, table counts or containers; the process list is not
// even read for them. A server admin gets all of it (BUGS RV47, A81).
func TestHostDetailsNeedServerConfig(t *testing.T) {
	sh := &shell{outputs: map[string]string{psCommand: psOutput, dfCommand: dfOutput}}
	s := linuxSite(t, sh, nil)
	for _, role := range []string{"viewer", "operator", "global_admin"} {
		cookie := s.signIn(role)
		for path := range hostDetails {
			if rec := s.call(cookie, "GET", path, ""); rec.Code != 403 || rec.Body.String() != `{"success":false,"error":"Permission denied: server.config"}` {
				t.Errorf("%s as %s: %d %s", path, role, rec.Code, rec.Body.String())
			}
		}
		rec := s.call(cookie, "GET", "/api/system/info", "")
		if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), `{"processes":[],"disks":[{"mount":"/",`) {
			t.Errorf("info as %s: %d %s", role, rec.Code, rec.Body.String())
		}
	}
	if strings.Join(sh.ran, "|") != strings.Repeat("5s "+dfCommand+"|", 2)+"5s "+dfCommand {
		t.Errorf("commands for the roles without server.config: %q", sh.ran)
	}
	sh.ran = nil
	admin := s.signIn("server_admin")
	if rec := s.call(admin, "GET", "/api/system/info", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `{"name":"/usr/bin/node /app/server.js --flag"`) {
		t.Errorf("info as server_admin: %d %s", rec.Code, rec.Body.String())
	}
	for path := range hostDetails {
		if rec := s.call(admin, "GET", path, ""); rec.Code != 200 {
			t.Errorf("%s as server_admin: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

var psOutput = `USER         PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
root          42 12.5  3.0 123456 65432 ?        Ssl  10:00   1:23 /usr/bin/node /app/server.js --flag
www-data       7  0.0  0.5   1234   567 ?        S    10:00   0:00 nginx: worker process
short line with too few fields
root           9  abc  1x5      0     0 ?        S    10:00   0:00 ` + "\tweird name " + strings.Repeat("n", 80) + `
`

const dfOutput = `/                  107374182400 53687091200 53687091200
/dev/shm             67108864        0    67108864
/sys/fs/cgroup              0        0           0
/proc/acpi                  0        0           0
/run/lock             5242880        0     5242880
/snap/core           58720256 58720256           0
/tiny                  524288     1024      523264
/data/big          2000000000000 1500000000000 500000000000
/mnt/odd name      1048576 x 5
three fields only
`

func TestInfoOnLinux(t *testing.T) {
	sh := &shell{outputs: map[string]string{psCommand: psOutput, dfCommand: dfOutput}}
	s := linuxSite(t, sh, nil)
	rec := s.call(s.signIn("server_admin"), "GET", "/api/system/info", "")
	want := `{"processes":[` +
		`{"name":"/usr/bin/node /app/server.js --flag","cpu":12.5,"mem":245.76},` +
		`{"name":"nginx: worker process","cpu":0,"mem":40.96},` +
		`{"name":"weird name ` + strings.Repeat("n", 53) + `","cpu":0,"mem":81.92}],` +
		`"disks":[{"mount":"/","total":107374182400,"used":53687091200,"free":53687091200},` +
		`{"mount":"/data/big","total":2000000000000,"used":1500000000000,"free":500000000000},` +
		`{"mount":"/mnt/odd","total":0,"used":1048576,"free":-1048576}],` +
		`"node":{"pid":4711,"uptime":90,"heapUsed":12,"heapTotal":20,"rss":64}}`
	// The mount with a space in its name shifts its fields: the old code
	// read them that way, and so the entry is dropped for its size of 0
	want = strings.Replace(want, `,{"mount":"/mnt/odd","total":0,"used":1048576,"free":-1048576}`, "", 1)
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("info:\n got  %d %s\n want 200 %s", rec.Code, rec.Body.String(), want)
	}
	if strings.Join(sh.ran, "\n") != "5s "+psCommand+"\n5s "+dfCommand {
		t.Errorf("commands: %q", sh.ran)
	}
	if rec := s.call(s.signIn("server_admin"), "HEAD", "/api/system/info", ""); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d with %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestInfoWithoutTheCommands(t *testing.T) {
	sh := &shell{}
	s := linuxSite(t, sh, nil)
	rec := s.call(s.signIn("server_admin"), "GET", "/api/system/info", "")
	if want := `{"processes":[],"disks":[],"node":{"pid":4711,"uptime":90,"heapUsed":12,"heapTotal":20,"rss":64}}`; rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestInfoOnWindowsAndElsewhere(t *testing.T) {
	sh := &shell{outputs: map[string]string{
		processCommand: "chrome|120.5|512.3\r\nSystem||0.1\r\nbroken|1\r\n",
		diskCommand:    "\r\nNode,DeviceID,FreeSpace,Size\r\nPC,C:,1000,5000\r\nPC,D:,,\r\nPC,,5,5\r\n",
	}}
	s := newSite(t, Options{Run: sh.run, Platform: "win32", OpenFile: opener(noFiles), Process: fixedProcess})
	rec := s.call(s.signIn("server_admin"), "GET", "/api/system/info", "")
	want := `{"processes":[{"name":"chrome","cpu":120.5,"mem":512.3},{"name":"System","cpu":0,"mem":0.1}],` +
		`"disks":[{"mount":"C:","name":"C:","total":5000,"used":4000,"free":1000}],` +
		`"node":{"pid":4711,"uptime":90,"heapUsed":12,"heapTotal":20,"rss":64}}`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("windows:\n got  %d %s\n want 200 %s", rec.Code, rec.Body.String(), want)
	}
	if strings.Join(sh.ran, "\n") != "10s "+processCommand+"\n10s "+diskCommand {
		t.Errorf("commands: %q", sh.ran)
	}

	other := &shell{}
	s = newSite(t, Options{Run: other.run, Platform: "darwin", OpenFile: opener(noFiles), Process: fixedProcess})
	rec = s.call(s.signIn("server_admin"), "GET", "/api/system/info", "")
	if !strings.HasPrefix(rec.Body.String(), `{"processes":[],"disks":[],"node":`) || len(other.ran) != 0 {
		t.Errorf("another system: %s, commands %q", rec.Body.String(), other.ran)
	}
}

func TestOwnProcess(t *testing.T) {
	info := ownProcess(time.Now().Add(-90 * time.Second))
	if info.PID != os.Getpid() || info.Uptime < 90 || info.Uptime > 95 || info.HeapUsed <= 0 || info.HeapTotal < info.HeapUsed || info.RSS <= 0 {
		t.Errorf("own process: %+v", info)
	}
}

func TestLogs(t *testing.T) {
	const console = "journalctl -u betterdesk-console --no-pager -n 50 --output=short-iso 2>/dev/null"
	const server5 = "journalctl -u betterdesk-server --no-pager -n 5 --output=short-iso 2>/dev/null || journalctl -u betterdesk-go --no-pager -n 5 --output=short-iso 2>/dev/null"
	sh := &shell{outputs: map[string]string{
		console: "2026-11-10T10:00:00+0000 host console[1]: started\n   \n2026-11-10T10:00:01+0000 host console[1]: \"quoted\"\n",
		server5: "one\n\ntwo",
	}}
	s := linuxSite(t, sh, nil)
	admin := s.signIn("server_admin")
	get := func(query string) string {
		t.Helper()
		rec := s.call(admin, "GET", "/api/logs/recent"+query, "")
		if rec.Code != 200 {
			t.Errorf("%s: %d", query, rec.Code)
		}
		return rec.Body.String()
	}
	if got, want := get(""), `{"source":"console","lines":["2026-11-10T10:00:00+0000 host console[1]: started","2026-11-10T10:00:01+0000 host console[1]: \"quoted\""]}`; got != want {
		t.Errorf("console log: %s", got)
	}
	if got := get("?source=go&limit=5"); got != `{"source":"go","lines":["one","two"]}` {
		t.Errorf("server log: %s", got)
	}

	// The limit as the command gets it, and the source as it goes back
	cases := []struct{ query, n, source string }{
		{"?limit=7", "7", `"console"`}, {"?limit=0", "50", `"console"`}, {"?limit=abc", "50", `"console"`}, {"?limit=1000", "200", `"console"`},
		{"?limit=12.9", "12", `"console"`}, {"?limit=%2015px", "15", `"console"`}, {"?limit=-3", "50", `"console"`}, {"?limit=3&limit=9", "3", `"console"`},
		{"?limit=-99999999999999999999999", "50", `"console"`}, {"?limit=1", "1", `"console"`},
		{"?limit=-" + strings.Repeat("9", 400), "50", `"console"`}, {"?limit=" + strings.Repeat("9", 400), "200", `"console"`},
		{"?source=", "50", `"console"`}, {"?source=kernel", "50", `"kernel"`}, {"?source=GO", "50", `"GO"`}, {"?source=a%22b+c", "50", `"a\"b c"`},
		{"?source=go&source=console", "50", `["go","console"]`}, {"?source[]=go", "50", `["go"]`}, {"?source%5Bname%5D=go&source[x]=1", "50", `{"name":"go","x":"1"}`},
	}
	for _, c := range cases {
		sh.ran = nil
		got := get(c.query)
		if !strings.HasPrefix(got, `{"source":`+c.source+`,"lines":[`) {
			t.Errorf("%s: %s, want the source %s", c.query, got, c.source)
		}
		want := "5s journalctl -u betterdesk-console --no-pager -n " + c.n + " --output=short-iso 2>/dev/null"
		if len(sh.ran) != 1 || sh.ran[0] != want {
			t.Errorf("%s ran %q, want %q", c.query, sh.ran, want)
		}
	}
}

func TestLogsFromFiles(t *testing.T) {
	files := map[string]string{
		"/opt/betterdesk/server.log":       "a\nb\n\nc\nd\n",
		"/var/log/betterdesk-server.log":   "never read: an earlier file exists",
		"/var/log/betterdesk-console.log":  "x\ny\nz",
		"/opt/rustdesk/betterdesk-go.log":  "not a file the route knows",
		"/opt/rustdesk/console.log.broken": "",
	}
	var opened []string
	read := func(path string) ([]byte, error) {
		opened = append(opened, path)
		if content, ok := files[path]; ok {
			return []byte(content), nil
		}
		return nil, fs.ErrNotExist
	}
	sh := &shell{}
	s := linuxSite(t, sh, read)
	admin := s.signIn("server_admin")
	for query, want := range map[string]string{
		"?source=go":          `{"source":"go","lines":["a","b","c","d"]}`,
		"?source=go&limit=3":  `{"source":"go","lines":["c","d"]}`,
		"?source=go&limit=-1": `{"source":"go","lines":["a","b","c","d"]}`,
		"?source=go&limit=-9": `{"source":"go","lines":["a","b","c","d"]}`,
		"?limit=2":            `{"source":"console","lines":["y","z"]}`,
		"?source=other":       `{"source":"other","lines":["x","y","z"]}`,
	} {
		if rec := s.call(admin, "GET", "/api/logs/recent"+query, ""); rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %s, want %s", query, rec.Code, rec.Body.String(), want)
		}
	}
	opened = nil
	s.call(admin, "GET", "/api/logs/recent?source=go", "")
	if strings.Join(opened, " ") != "/opt/rustdesk/betterdesk-server.log /opt/betterdesk/server.log" {
		t.Errorf("files tried: %q", opened)
	}

	// A file that is there and cannot be read fails the request
	denied := linuxSite(t, &shell{}, func(string) ([]byte, error) { return nil, fs.ErrPermission })
	if rec := denied.call(denied.signIn("server_admin"), "GET", "/api/logs/recent", ""); rec.Code != 500 || rec.Body.String() != `{"error":"Failed to read logs"}` {
		t.Errorf("unreadable file: %d %s", rec.Code, rec.Body.String())
	}
	if rec := denied.call(denied.signIn("server_admin"), "GET", "/api/logs/recent?limit[toString]=1", ""); rec.Code != 500 || rec.Body.String() != `{"error":"Failed to read logs"}` {
		t.Errorf("a limit that is no text: %d %s", rec.Code, rec.Body.String())
	}
	// Nothing anywhere, and no journal on other systems
	other := &shell{}
	elsewhere := newSite(t, Options{Run: other.run, Platform: "win32", OpenFile: opener(noFiles), Process: fixedProcess})
	if rec := elsewhere.call(elsewhere.signIn("server_admin"), "GET", "/api/logs/recent?source=go", ""); rec.Body.String() != `{"source":"go","lines":[]}` || len(other.ran) != 0 {
		t.Errorf("windows: %s, commands %q", rec.Body.String(), other.ran)
	}
}

// countingFile is a file in memory that counts the bytes read from it.
type countingFile struct {
	*bytes.Reader
	read *int
}

func (f countingFile) Read(p []byte) (int, error) {
	n, err := f.Reader.Read(p)
	*f.read += n
	return n, err
}

func (countingFile) Close() error { return nil }

// TestLogsReadOnlyTheEndOfALargeFile: the log route reads the end of a log
// file, not all of it, and still gives its last lines, also where a line
// is cut by the blocks it reads (BUGS.md SC13).
func TestLogsReadOnlyTheEndOfALargeFile(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 200000; i++ {
		fmt.Fprintf(&content, "line %06d %s\n", i, strings.Repeat("ü", 40))
	}
	read := 0
	open := func(path string) (io.ReadSeekCloser, error) {
		if path != "/opt/rustdesk/betterdesk-server.log" {
			return nil, fs.ErrNotExist
		}
		return countingFile{bytes.NewReader([]byte(content.String())), &read}, nil
	}
	s := newSite(t, Options{Run: (&shell{}).run, Platform: "linux", TotalMemory: func() uint64 { return 8 << 30 }, OpenFile: open, Process: fixedProcess})
	admin := s.signIn("server_admin")
	// The line end at the end of the file counts as a line, as it did
	for limit, first := range map[string]int{"3": 199998, "200": 199801, "": 199951} {
		read = 0
		rec := s.call(admin, "GET", "/api/logs/recent?source=go&limit="+limit, "")
		var got struct{ Lines []string }
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
			t.Fatalf("limit %q: %d %s", limit, rec.Code, rec.Body.String())
		}
		if len(got.Lines) != 200000-first || !strings.HasPrefix(got.Lines[0], fmt.Sprintf("line %06d ", first)) || !strings.HasPrefix(got.Lines[len(got.Lines)-1], "line 199999 ") {
			t.Errorf("limit %q: %d lines from %q", limit, len(got.Lines), got.Lines[0])
		}
		if read > 1<<20 {
			t.Errorf("limit %q: %d bytes read of a file of %d", limit, read, content.Len())
		}
	}

	// A file with a line too long to read: the read stops at 8 MiB and
	// leaves out the line it cut
	content.Reset()
	content.WriteString(strings.Repeat("x", 9<<20) + "\na\nb\n")
	read = 0
	if rec := s.call(admin, "GET", "/api/logs/recent?source=go", ""); rec.Body.String() != `{"source":"go","lines":["a","b"]}` || read > 9<<20 {
		t.Errorf("a long line: %d bytes read, %.80s", read, rec.Body.String())
	}
}

// TestDatabaseStatsAnswersTheTables: the database statistics are the type
// of the database, the row count of each table and the size, where the
// route always failed with 500 (BUGS.md C1).
func TestDatabaseStatsAnswersTheTables(t *testing.T) {
	s := linuxSite(t, &shell{}, nil)
	rec := s.call(s.signIn("admin"), "GET", "/api/database/stats", "")
	var got struct {
		Type   string `json:"type"`
		Tables []struct {
			Name  string `json:"name"`
			Count any    `json:"count"`
		} `json:"tables"`
		Size string `json:"size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body.String(), err)
	}
	counts := map[string]any{}
	for _, table := range got.Tables {
		counts[table.Name] = table.Count
	}
	if got.Type != "SQLite" || got.Size != "-" || counts["panel_sessions"] != 1.0 {
		t.Errorf("stats: %s", rec.Body.String())
	}
}

func TestSafeSQLIdent(t *testing.T) {
	for _, name := range []string{"users", "settings", "peer", "audit_log", "_x", "T1"} {
		if got, err := assertSafeSQLIdent(name); err != nil || got != name || !isSafeSQLIdent(name) {
			t.Errorf("%q is refused: %v", name, err)
		}
	}
	for _, name := range []string{`users"; DROP TABLE users--`, `users" OR 1=1 --`, "../etc/passwd", "users;--", "", "1users", "us ers", "users\n", "ünits", "a-b", "a.b"} {
		if _, err := assertSafeSQLIdent(name); isSafeSQLIdent(name) || err == nil || !strings.HasPrefix(err.Error(), "Unsafe SQL identifier: ") {
			t.Errorf("%q is accepted (%v)", name, err)
		}
	}
}

func TestCollectStats(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, ddl := range []string{
			`CREATE TABLE stats_plain (id INTEGER)`,
			`CREATE TABLE "stats odd; name" (id INTEGER)`,
			`INSERT INTO stats_plain (id) VALUES (1), (2), (3)`,
			`INSERT INTO "stats odd; name" (id) VALUES (1)`,
		} {
			if _, err := db.Exec(ctx, ddl); err != nil {
				t.Fatal(err)
			}
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "main.db")
		if err := os.WriteFile(file, make([]byte, 3*1024*1024/2), 0o600); err != nil {
			t.Fatal(err)
		}
		backups := filepath.Join(dir, "backups")
		_ = os.Mkdir(backups, 0o755)
		for _, name := range []string{"2026-01-01.db", "2026-03-01.sql", "2026-02-01.gz", "9999-notes.txt"} {
			if err := os.WriteFile(filepath.Join(backups, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		newest := time.Date(2026, 3, 1, 4, 5, 6, 789e6, time.UTC)
		_ = os.Chtimes(filepath.Join(backups, "2026-03-01.sql"), newest, newest)

		h := newHandlers(kit.NewLogger("error", true), db, Options{DatabaseFile: file, BackupDir: backups})
		stats, err := h.collectStats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		listed := map[string]any{}
		for _, table := range stats.Tables {
			listed[table.Name] = table.Count
			if strings.Contains(table.Name, "odd") {
				t.Errorf("the table with an unsafe name is listed: %q", table.Name)
			}
		}
		plain, err := countRows(ctx, db, "stats_plain")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := countRows(ctx, db, "stats odd; name"); err == nil || !strings.HasPrefix(err.Error(), "Unsafe SQL identifier") {
			t.Errorf("counting a table with an unsafe name: %v", err)
		}
		if _, err := countRows(ctx, db, "stats_missing"); err == nil {
			t.Error("counting a table that is not there gave no error")
		}
		if db.IsPostgres() {
			// The tests have a schema of their own, and the list is that
			// of the schema "public": only the kind of answer is checked
			if stats.Type != "PostgreSQL" || plain.Count != "3" || !strings.Contains(stats.Size, "B") {
				t.Errorf("type %q, count %#v, size %q", stats.Type, plain.Count, stats.Size)
			}
		} else if stats.Type != "SQLite" || plain.Count != int64(3) || listed["stats_plain"] != int64(3) || stats.Size != "1.5 MB" {
			t.Errorf("type %q, count %#v, listed %v, size %q", stats.Type, plain.Count, listed, stats.Size)
		}
		if stats.LastBackup != "2026-03-01T04:05:06.789Z" {
			t.Errorf("last backup %q", stats.LastBackup)
		}

		// A small file is given in KB, and no backup folder means no time
		_ = os.WriteFile(file, make([]byte, 2560), 0o600)
		h = newHandlers(kit.NewLogger("error", true), db, Options{DatabaseFile: file, BackupDir: filepath.Join(dir, "none")})
		if stats, _ = h.collectStats(ctx); stats.LastBackup != "" || (!db.IsPostgres() && stats.Size != "3 KB") {
			t.Errorf("small file: size %q, last backup %q", stats.Size, stats.LastBackup)
		}
	})
}

func TestContainers(t *testing.T) {
	sh := &shell{outputs: map[string]string{dockerCommand: "web|nginx:1.27|running|Up 3 hours|0.0.0.0:80->80/tcp, 443/tcp\n\nold|alpine|exited|Exited (0) 2 days ago|\nbare\na|b|c|d|e|f\n"}}
	s := linuxSite(t, sh, nil)
	admin := s.signIn("server_admin")
	rec := s.call(admin, "GET", "/api/docker/containers", "")
	want := `{"containers":[{"name":"web","image":"nginx:1.27","state":"running","status":"Up 3 hours","ports":"0.0.0.0:80->80/tcp, 443/tcp"},` +
		`{"name":"old","image":"alpine","state":"exited","status":"Exited (0) 2 days ago","ports":""},` +
		`{"name":"bare","image":"","state":"","status":"","ports":""},{"name":"a","image":"b","state":"c","status":"d","ports":"e"}],"available":true}`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("containers:\n got  %d %s\n want 200 %s", rec.Code, rec.Body.String(), want)
	}
	if len(sh.ran) != 1 || sh.ran[0] != "10s "+dockerCommand {
		t.Errorf("commands: %q", sh.ran)
	}
	for name, sh := range map[string]*shell{"no docker": {}, "docker prints nothing": {outputs: map[string]string{dockerCommand: " \n"}}} {
		s := linuxSite(t, sh, nil)
		if rec := s.call(s.signIn("server_admin"), "GET", "/api/docker/containers", ""); rec.Code != 200 || rec.Body.String() != `{"containers":[],"available":false}` {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestExec(t *testing.T) {
	sh := &shell{outputs: map[string]string{"whoami": "root\n", "uptime": " 12:00:00 up 3 days\n", "date": "", "top -bn1 | head -20": "top - \"12:00\"\n\tTasks: 5 "}}
	s := linuxSite(t, sh, nil)
	admin := s.signIn("admin")
	const refused = `{"error":"Command not in allowed list. Allowed: uptime, date, hostname, whoami, df -h, free -m, uname -a, cat /etc/os-release, systemctl status betterdesk-server, systemctl status betterdesk-console, docker ps, docker stats --no-stream, ip addr, ss -tlnp, netstat -tlnp, top -bn1 | head -20"}`
	cases := []struct {
		body   string
		status int
		want   string
		ran    string
	}{
		{`{"command":"whoami"}`, 200, `{"output":"root","command":"whoami"}`, "10s whoami"},
		{`{"command":"  uptime\n"}`, 200, `{"output":"12:00:00 up 3 days","command":"uptime"}`, "10s uptime"},
		{`{"command":"date"}`, 200, `{"output":"(no output)","command":"date"}`, "10s date"},
		{`{"command":"hostname"}`, 200, `{"output":"(no output)","command":"hostname"}`, "10s hostname"},
		{`{"command":"top -bn1 | head -20"}`, 200, `{"output":"top - \"12:00\"\n\tTasks: 5","command":"top -bn1 | head -20"}`, "10s top -bn1 | head -20"},
		{`{}`, 400, `{"error":"Command is required"}`, ""},
		{`{"command":""}`, 400, `{"error":"Command is required"}`, ""},
		{`{"command":["whoami"]}`, 400, `{"error":"Command is required"}`, ""},
		{`{"command":7}`, 400, `{"error":"Command is required"}`, ""},
		{`{"command":" "}`, 403, refused, ""},
		{`{"command":"whoami; id"}`, 403, refused, ""},
		{`{"command":"WHOAMI"}`, 403, refused, ""},
		{`{"command":"df  -h"}`, 403, refused, ""},
		{`{"command":"top -bn1"}`, 403, refused, ""},
		{`{"command":"cat /etc/passwd"}`, 403, refused, ""},
	}
	for _, c := range cases {
		sh.ran = nil
		status, answer := s.post(admin, "/api/system/exec", c.body)
		if status != c.status || answer != c.want {
			t.Errorf("%s\n got  %d %s\n want %d %s", c.body, status, answer, c.status, c.want)
		}
		if strings.Join(sh.ran, ";") != c.ran {
			t.Errorf("%s ran %q, want %q", c.body, sh.ran, c.ran)
		}
	}
}

func TestSpeedTest(t *testing.T) {
	s := linuxSite(t, &shell{}, nil)
	viewer := s.signIn("viewer")
	for query, size := range map[string]int{
		"": 1048576, "?size=10": 10, "?size=65536": 65536, "?size=65537": 65537, "?size=0": 1048576, "?size=abc": 1048576, "?size=2048kb": 2048,
		"?size=99999999": 10485760, "?size=5.9": 5, "?size=7&size=9": 7, "?size[]=11": 11, "?size[a]=1": 1048576, "?size=-5": 1048576, "?size=-0": 1048576,
	} {
		rec := s.call(viewer, "GET", "/api/speed-test"+query, "")
		h := rec.Header()
		if rec.Code != 200 || rec.Body.Len() != size || strings.Trim(rec.Body.String(), "B") != "" {
			t.Errorf("%s: %d with %d bytes, want %d bytes B", query, rec.Code, rec.Body.Len(), size)
		}
		if h.Get("Content-Type") != "application/octet-stream" || h.Get("Cache-Control") != "no-store" || h.Get("Content-Length") != itoa(size) || h.Get("ETag") != "" {
			t.Errorf("%s: headers %v", query, h)
		}
	}
	rec := s.call(viewer, "HEAD", "/api/speed-test?size=10", "")
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "10" {
		t.Errorf("HEAD: %d, %d bytes, headers %v", rec.Code, rec.Body.Len(), rec.Header())
	}
}

func itoa(n int) string { return numberText(float64(n)) }

func TestRunShell(t *testing.T) {
	ctx := context.Background()
	if out, err := runShell(ctx, "printf 'a b' | tr a-z A-Z; echo err >&2", time.Second*5); err != nil || out != "A B" {
		t.Errorf("pipe: %q, %v", out, err)
	}
	if out, err := runShell(ctx, "echo partial; exit 3", 5*time.Second); err == nil {
		t.Errorf("a failing command gave %q without an error", out)
	}
	if _, err := runShell(ctx, "surely-not-a-command-7f3a", 5*time.Second); err == nil {
		t.Error("a missing command gave no error")
	}
	start := time.Now()
	if _, err := runShell(ctx, "sleep 5", 200*time.Millisecond); err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("a slow command: %v after %s", err, time.Since(start))
	}
	// Through the route: what the command printed, trimmed, or nothing
	h := newHandlers(kit.NewLogger("error", true), nil, Options{})
	if got := h.run(ctx, "echo ' hi '", time.Second*5); got != "hi" {
		t.Errorf("run: %q", got)
	}
	if got := h.run(ctx, "echo out; false", time.Second*5); got != "" {
		t.Errorf("run of a failing command: %q", got)
	}
}

func TestTextRules(t *testing.T) {
	if got := fields("a  b\tc"); strings.Join(got, "|") != "a|b|c" {
		t.Errorf("fields: %q", got)
	}
	if got := fields(""); len(got) != 1 || got[0] != "" {
		t.Errorf("fields of nothing: %q", got)
	}
	for in, want := range map[string]float64{"12.5": 12.5, " 3x": 3, "abc": 0, "": 0, "1e2": 100, "1e": 1, ".5": 0.5, "5.": 5, "-2.5%": -2.5, "+7": 7, "-": 0, ".": 0, "0x10": 0, "1.2.3": 1.2} {
		if got := parseFloat(in); got != want {
			t.Errorf("parseFloat(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[float64]string{0: "0", 200: "200", -3: "-3", 1e21: "1e+21", -1e23: "-1e+23", 123456789012345680000: "123456789012345680000", 1.5e300: "1.5e+300", math.Inf(1): "Infinity", math.Inf(-1): "-Infinity"} {
		if got := numberText(in); got != want {
			t.Errorf("numberText(%v) = %q, want %q", in, got, want)
		}
	}
	if got := substring("ab\U0001F600cd", 3); got != "ab" {
		t.Errorf("substring cuts a pair in half: %q", got)
	}
	if got := substring("ab\U0001F600cd", 5); got != "ab\U0001F600c" {
		t.Errorf("substring: %q", got)
	}
}

// FuzzCommandOutput feeds text to everything that reads the output of a
// command or a query string. Nothing may panic, and what goes into an
// answer must be text.
func FuzzCommandOutput(f *testing.F) {
	for _, seed := range []string{psOutput, dfOutput, "a|b|c", "Node,DeviceID,FreeSpace,Size\r\nPC,C:,1,2", "source=go&limit=5", "source[]=a&source[b]=c&size=%zz", "\xff\xfe", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		text := trim(decodeUTF8([]byte(raw)))
		if !utf8.ValidString(text) {
			t.Fatalf("decodeUTF8(%q) is not UTF-8", raw)
		}
		for _, p := range parsePS(text, 8<<30) {
			if n := len([]rune(string(p.Name))); n > 64 {
				t.Fatalf("a process name of %d characters", n)
			}
		}
		for _, d := range parseDF(text) {
			if d.Total < megabyte || d.Free != d.Total-d.Used {
				t.Fatalf("disk %+v", d)
			}
		}
		parseGetProcess(text)
		for _, d := range parseWMIC(text) {
			if d.Total <= 0 || d.Mount == "" {
				t.Fatalf("disk %+v", d)
			}
		}
		if limit := logLimit(text); limit < 1 || limit > 200 || float64(len(lastLines(strings.Split(text, "\n"), limit))) > limit {
			t.Fatalf("limit %v of %q", limit, text)
		}
		switch v := queryValue(raw, "source").(type) {
		case nil, string, []string:
		case orderedObject:
			if _, err := v.MarshalJSON(); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("queryValue gave a %T", v)
		}
		_, _ = queryText(raw, "size")
		numberText(parseIntOr(text, 1))
	})
}
