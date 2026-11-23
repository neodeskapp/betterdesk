package servicedef

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// The vectors were recorded from the Node console (testdata/sanitize.json,
// see testdata/README.md).
type vectors struct {
	Text []struct {
		Fn     string          `json:"fn"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
	} `json:"text"`
	Flows []struct {
		Windows bool              `json:"windows"`
		Script  script            `json:"script"`
		Result  json.RawMessage   `json:"result"`
		Log     []logEntry        `json:"log"`
		Files   map[string]string `json:"files"`
	} `json:"flows"`
}

type script struct {
	Shell      map[string]json.RawMessage `json:"shell"`
	Files      map[string]string          `json:"files"`
	ReadError  map[string]string          `json:"readError"`
	WriteError string                     `json:"writeError"`
	ExecFail   string                     `json:"execFail"`
	ReloadFail string                     `json:"reloadFail"`
}

type logEntry struct {
	Shell      string   `json:"shell,omitempty"`
	Exec       []string `json:"exec,omitempty"`
	Write      string   `json:"write,omitempty"`
	Data       string   `json:"data,omitempty"`
	Privileged string   `json:"privileged,omitempty"`
}

func load(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sanitize.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestTextVectors replays the text helpers of the unit and of the
// Windows environment block.
func TestTextVectors(t *testing.T) {
	v := load(t)
	if len(v.Text) < 150 {
		t.Fatalf("only %d vectors", len(v.Text))
	}
	for _, c := range v.Text {
		var args []json.RawMessage
		if err := json.Unmarshal(c.Args, &args); err != nil {
			t.Fatal(err)
		}
		text := func(i int) string {
			var s string
			_ = json.Unmarshal(args[i], &s)
			return s
		}
		var got any
		switch c.Fn {
		case "stripIncompatibleGoApiTLSArgs":
			var compact bool
			_ = json.Unmarshal(args[1], &compact)
			got = StripIncompatibleGoAPITLSArgs(text(0), compact)
		case "ensureMeshEnabledInServiceEnv":
			s, changed := EnsureMeshEnabledInServiceEnv(text(0))
			got = map[string]any{"text": s, "changed": changed}
		case "ensureGoServerEnvironmentFile":
			s, changed := EnsureGoServerEnvironmentFile(text(0), text(1))
			got = map[string]any{"text": s, "changed": changed}
		case "ensureGoServerSignalRelayPorts":
			s, changed := EnsureGoServerSignalRelayPorts(text(0))
			got = map[string]any{"text": s, "changed": changed}
		case "mergeBillingEnvIntoWindowsServiceExtra":
			var vars map[string]string
			_ = json.Unmarshal(args[1], &vars)
			s, changed := MergeBillingEnvIntoWindowsServiceExtra(text(0), vars)
			got = map[string]any{"text": s, "changed": changed}
		case "parseEnvFileKeys":
			var keys []string
			_ = json.Unmarshal(args[1], &keys)
			got = ParseEnvFileKeys(text(0), keys)
		default:
			t.Fatalf("unknown function %s", c.Fn)
		}
		if !sameJSON(t, got, c.Result) {
			gotText, _ := json.Marshal(got)
			t.Errorf("%s%s:\n got %s\nNode %s", c.Fn, c.Args, gotText, c.Result)
		}
	}
}

func sameJSON(t *testing.T, got any, want json.RawMessage) bool {
	t.Helper()
	var a, b any
	text, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(text, &a) != nil || json.Unmarshal(want, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// fileInfo is enough of an os.FileInfo for Stat.
type fileInfo struct{ name string }

func (f fileInfo) Name() string       { return f.name }
func (f fileInfo) Size() int64        { return 0 }
func (f fileInfo) Mode() fs.FileMode  { return 0o644 }
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return false }
func (f fileInfo) Sys() any           { return nil }

// TestFlowVectors replays whole checks on Linux and Windows with the
// commands, files and failures each vector scripts: the result, the
// commands and writes in their order, and the files after.
func TestFlowVectors(t *testing.T) {
	v := load(t)
	if len(v.Flows) < 60 {
		t.Fatalf("only %d flows", len(v.Flows))
	}
	for i, flow := range v.Flows {
		files := map[string]string{}
		for name, content := range flow.Script.Files {
			files[name] = content
		}
		var log []logEntry
		sc := flow.Script
		s := New(Options{
			Windows:        flow.Windows,
			ServerService:  map[bool]string{true: "NeoDeskServer", false: "neodesk-server"}[flow.Windows],
			ConsoleService: map[bool]string{true: "NeoDeskConsole", false: "neodesk-console"}[flow.Windows],
			ConsoleEnvPath: "/opt/console/.env",
			Shell: func(_ context.Context, command string) (string, error) {
				log = append(log, logEntry{Shell: command})
				raw, ok := sc.Shell[command]
				if !ok {
					return "", errors.New("Command failed: " + command)
				}
				var out string
				if json.Unmarshal(raw, &out) == nil {
					return out, nil
				}
				var failed struct {
					Fail string `json:"fail"`
				}
				_ = json.Unmarshal(raw, &failed)
				return "", errors.New(failed.Fail)
			},
			Exec: func(_ context.Context, name string, args ...string) error {
				log = append(log, logEntry{Exec: append([]string{name}, args...)})
				if sc.ExecFail != "" {
					return errors.New(sc.ExecFail)
				}
				return nil
			},
			DaemonReload: func(context.Context) error {
				log = append(log, logEntry{Privileged: "systemctl daemon-reload"})
				if sc.ReloadFail != "" {
					return errors.New(sc.ReloadFail)
				}
				return nil
			},
			Stat: func(name string) (os.FileInfo, error) {
				if _, ok := files[name]; !ok {
					return nil, fs.ErrNotExist
				}
				return fileInfo{name: name}, nil
			},
			ReadFile: func(name string) ([]byte, error) {
				if sc.ReadError[name] != "" {
					return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
				}
				content, ok := files[name]
				if !ok {
					return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.ENOENT}
				}
				return []byte(content), nil
			},
			WriteFile: func(name string, data []byte) error {
				log = append(log, logEntry{Write: name, Data: string(data)})
				if sc.WriteError != "" {
					return &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
				}
				files[name] = string(data)
				return nil
			},
		})
		result := s.Check(context.Background())
		if !sameJSON(t, result, withUnitHint(flow.Result)) {
			got, _ := json.Marshal(result)
			t.Errorf("flow %d (windows %v): result\n got %s\nNode %s", i, flow.Windows, got, flow.Result)
		}
		if !sameJSON(t, log, mustRaw(t, flow.Log)) {
			got, _ := json.Marshal(log)
			want, _ := json.Marshal(flow.Log)
			t.Errorf("flow %d: commands and writes\n got %s\nNode %s", i, got, want)
		}
		if !reflect.DeepEqual(files, flow.Files) {
			t.Errorf("flow %d: files\n got %q\nNode %q", i, files, flow.Files)
		}
	}
}

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	text, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// TestRunShellAnswersAsExecSync pins the messages of the default runner.
func TestRunShellAnswersAsExecSync(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	out, err := RunShell(context.Background(), "printf 'a\\n'; echo b >&2")
	if err != nil || out != "a\n" {
		t.Fatalf("RunShell = %q, %v", out, err)
	}
	_, err = RunShell(context.Background(), "echo oops >&2; exit 3")
	if err == nil || err.Error() != "Command failed: echo oops >&2; exit 3\noops\n" {
		t.Errorf("a failing command gives %v", err)
	}
	out, err = RunShell(context.Background(), "systemctl-does-not-exist show x 2>/dev/null || true")
	if err != nil || out != "" {
		t.Errorf("the guarded command gives %q, %v", out, err)
	}
}
