package servermgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// nodeCommands is testdata/node_commands.json, recorded from the old
// serverManagement.js with child_process replaced (testdata/README.md).
type nodeCommands struct {
	Disks []struct {
		Platform string `json:"platform"`
		Stdout   string `json:"stdout"`
		Status   int    `json:"status"`
		Failed   bool   `json:"failed"`
		Disks    any    `json:"disks"`
		Calls    []call `json:"calls"`
	} `json:"disks"`
	Swap []struct {
		Meminfo *string `json:"meminfo"`
		Swap    any     `json:"swap"`
	} `json:"swap"`
	Services []struct {
		Platform string `json:"platform"`
		Stdout   string `json:"stdout"`
		Status   int    `json:"status"`
		Services any    `json:"services"`
		Calls    []call `json:"calls"`
	} `json:"services"`
	Control []struct {
		Platform string `json:"platform"`
		Broker   bool   `json:"broker"`
		Name     string `json:"name"`
		Action   string `json:"action"`
		Answer   string `json:"answer"`
		Result   any    `json:"result"`
		Error    string `json:"error"`
		Calls    []call `json:"calls"`
	} `json:"control"`
}

// call is a command the old service started (or a timer it set).
type call struct {
	Sync    bool     `json:"sync"`
	Cmd     string   `json:"cmd"`
	Args    []string `json:"args"`
	Timeout float64  `json:"timeout"`
	Timer   float64  `json:"timer"`
	Killed  any      `json:"killed"`
}

func loadNodeCommands(t *testing.T) nodeCommands {
	t.Helper()
	raw, err := os.ReadFile("testdata/node_commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec nodeCommands
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func goos(platform string) string {
	if platform == "win32" {
		return "windows"
	}
	return platform
}

// asJSON makes a Go value comparable with a recorded one.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// TestNodeDisksAndSwap replays the disks the old service read from df and
// wmic, and the swap from /proc/meminfo.
func TestNodeDisksAndSwap(t *testing.T) {
	rec := loadNodeCommands(t)
	for i, v := range rec.Disks {
		var got []Disk
		if !v.Failed && v.Status == 0 {
			text := jsbind.Trim(v.Stdout)
			if v.Platform == "linux" {
				got = parseDF(text)
			} else {
				got = parseWMIC(text)
			}
		}
		if want := v.Disks; !reflect.DeepEqual(asJSON(t, nonNil(got)), want) {
			t.Errorf("disks %d (%s %q): got %v, want %v", i, v.Platform, v.Stdout, asJSON(t, got), want)
		}
		wantCmd := "df"
		wantTimeout := 4000.0
		if v.Platform == "win32" {
			wantCmd, wantTimeout = "wmic", 8000
		}
		if len(v.Calls) != 1 || v.Calls[0].Cmd != wantCmd || v.Calls[0].Timeout != wantTimeout {
			t.Errorf("disks %d: the recording ran %+v", i, v.Calls)
		}
	}
	for i, v := range rec.Swap {
		var swap Swap
		if v.Meminfo != nil {
			_, _, swap = parseMeminfo(*v.Meminfo)
		}
		if got := asJSON(t, swap); !reflect.DeepEqual(got, v.Swap) {
			t.Errorf("swap %d (%q): got %v, want %v", i, deref(v.Meminfo), got, v.Swap)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

// fakeRun answers commands from a table and notes what ran.
type fakeRun struct {
	stdout, stderr string
	code           int
	err            error
	// hang waits for the time limit and answers as a killed command
	hang  bool
	calls []string
	limit time.Duration
}

func (f *fakeRun) run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if deadline, ok := ctx.Deadline(); ok {
		f.limit = time.Until(deadline).Round(time.Second)
	}
	if f.hang {
		return nil, nil, -1, nil
	}
	return []byte(f.stdout), []byte(f.stderr), f.code, f.err
}

// crlfServices is the list the Go console makes of the recording's
// PowerShell output with CR LF line ends, as PowerShell writes them: every
// allowed service in it (SM8). The old service matched only the last line,
// which has no line end.
var crlfServices = []any{
	map[string]any{"name": "NeoDeskServer", "active": "active", "sub": "running", "description": "NeoDesk Server"},
	map[string]any{"name": "NeoDeskConsole", "active": "inactive", "sub": "stopped", "description": "NeoDesk Console"},
}

// TestNodeServiceList replays the service lists the old service made of
// systemctl's and PowerShell's output: the allow list, the parsing, a
// command that failed. Where the Go console differs on purpose, the list
// is changed here: a PowerShell list with CR LF line ends (crlfServices).
func TestNodeServiceList(t *testing.T) {
	rec := loadNodeCommands(t)
	crlf := 0
	for i, v := range rec.Services {
		want := v.Services
		if v.Platform == "win32" && v.Status == 0 && strings.Contains(v.Stdout, "\r\n") {
			want = crlfServices
			crlf++
		}
		f := &fakeRun{stdout: v.Stdout, code: v.Status}
		s := New(Options{GOOS: goos(v.Platform), Run: f.run})
		got := []any{}
		for _, u := range s.ListServices(context.Background()) {
			o := map[string]any{"name": u.Name}
			if u.Load != nil {
				o["load"] = *u.Load
			}
			o["active"], o["sub"], o["description"] = u.Active, u.Sub, u.Description
			got = append(got, o)
		}
		if !reflect.DeepEqual(asJSON(t, got), want) {
			t.Errorf("list %d (%s %q):\n got %v\nwant %v", i, v.Platform, v.Stdout, asJSON(t, got), want)
		}
		var wantCalls []string
		for _, c := range v.Calls {
			wantCalls = append(wantCalls, c.Cmd+" "+strings.Join(c.Args, " "))
		}
		if !reflect.DeepEqual(f.calls, wantCalls) {
			t.Errorf("list %d ran %q, want %q", i, f.calls, wantCalls)
		}
	}
	if crlf != 1 {
		t.Errorf("%d lists with CR LF, the recording has one", crlf)
	}
}

// TestNodeServiceControl replays every name and action the oracle sent to
// controlService on Linux, Windows and another system: the refusals, the
// command line, the time limit, and the result for a command that
// succeeds, fails, prints a lot, ends by a signal or hangs. The recordings
// with the privileged helper are not replayed: the Go console has none
// (spec updater-installers 4.6).
func TestNodeServiceControl(t *testing.T) {
	rec := loadNodeCommands(t)
	n := 0
	for i, v := range rec.Control {
		if v.Broker {
			continue
		}
		n++
		f := &fakeRun{}
		switch v.Answer {
		case "ok":
			f.stdout = "done\n"
		case "fails":
			f.stderr, f.code = "Failed to start: Unit not found.\n", 5
		case "long output":
			f.stdout, f.stderr = strings.Repeat("x", 5000)+"END", strings.Repeat("\u00e9", 4100)
		case "signal":
			f.code = -1
		case "hangs":
			f.hang = true
		}
		s := New(Options{GOOS: goos(v.Platform), Run: f.run})
		res, err := s.ControlService(context.Background(), v.Name, v.Action)
		where := fmt.Sprintf("control %d (%s %q %q, %s)", i, v.Platform, v.Name, v.Action, v.Answer)
		if v.Error != "" || err != nil {
			if err == nil || err.Error() != v.Error {
				t.Errorf("%s: error %v, want %q", where, err, v.Error)
			}
		} else if got := asJSON(t, res); !reflect.DeepEqual(got, v.Result) {
			t.Errorf("%s:\n got %.300v\nwant %.300v", where, got, v.Result)
		}
		var wantCalls []string
		var wantLimit time.Duration
		for _, c := range v.Calls {
			if c.Timer != 0 {
				wantLimit = time.Duration(c.Timer) * time.Millisecond
				continue
			}
			wantCalls = append(wantCalls, c.Cmd+" "+strings.Join(c.Args, " "))
		}
		if !reflect.DeepEqual(f.calls, wantCalls) {
			t.Errorf("%s: ran %q, want %q", where, f.calls, wantCalls)
		}
		if len(wantCalls) > 0 && f.limit != wantLimit {
			t.Errorf("%s: time limit %v, want %v", where, f.limit, wantLimit)
		}
	}
	if n < 1000 {
		t.Errorf("only %d control vectors", n)
	}
}
