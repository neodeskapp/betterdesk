package restart

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// The vectors are lives of a restart transaction as
// services/restartCoordinator.js of the Node console lived them
// (testdata/nodeoracle/restart.js): per step the call, what it returned
// or threw, the transaction file afterwards, what it asked of the other
// services, and whether it scheduled the end of the process.

type vectorScript struct {
	Restart struct {
		Result json.RawMessage `json:"result"`
		Throw  *string         `json:"throw"`
	} `json:"restart"`
	RollbackErrors map[string]string `json:"rollbackErrors"`
	SetConfig      struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	} `json:"setConfig"`
	Health string `json:"health"`
}

type vectorStep struct {
	Op      string  `json:"op"`
	User    int64   `json:"user"`
	ID      string  `json:"id"`
	Token   string  `json:"token"`
	Key     string  `json:"key"`
	Content *string `json:"content"`
	Change  *struct {
		Key      string          `json:"key"`
		Label    string          `json:"label"`
		Rollback json.RawMessage `json:"rollback"`
	} `json:"change"`
	Script vectorScript `json:"script"`

	Out *string `json:"out"`
	Err *struct {
		Message    string  `json:"message"`
		Type       string  `json:"type"`
		StatusCode int     `json:"statusCode"`
		Details    *string `json:"details"`
	} `json:"err"`
	File  *string           `json:"file"`
	Calls []json.RawMessage `json:"calls"`
	Exit  bool              `json:"exit"`
	Dir   []string          `json:"dir"`
}

type vectorFile struct {
	Node      string `json:"node"`
	Scenarios []struct {
		Name  string       `json:"name"`
		Steps []vectorStep `json:"steps"`
	} `json:"scenarios"`
}

func loadVectors(t testing.TB) vectorFile {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "vectors.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var file vectorFile
	if err := json.NewDecoder(zr).Decode(&file); err != nil {
		t.Fatal(err)
	}
	if len(file.Scenarios) == 0 {
		t.Fatal("no scenarios in the vector file")
	}
	return file
}

// oracleWorld is the Service with the counters the Node script ran on: a
// clock that moves one second per reading, random bytes that count up,
// and fakes for the server's API, the restart and the rollbacks.
type oracleWorld struct {
	t      testing.TB
	dir    string
	tick   int
	random int
	script vectorScript
	calls  []string
	exit   bool
	svc    *Service
}

func (w *oracleWorld) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(w.random*73 + 41)
		w.random++
	}
	return len(p), nil
}

func (w *oracleWorld) now() time.Time {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(w.tick) * time.Second)
	w.tick++
	return now
}

func (w *oracleWorld) call(parts ...any) {
	text, err := json.Marshal(parts)
	if err != nil {
		w.t.Fatal(err)
	}
	w.calls = append(w.calls, string(text))
}

// api is the server's API: the configuration value a go-config rollback
// writes, and the health.
func (w *oracleWorld) api(rw http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.EscapedPath(), "/api/config/"):
		key, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/api/config/"))
		if err != nil {
			w.t.Errorf("config key: %v", err)
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Value any `json:"value"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			w.t.Errorf("config body %q: %v", raw, err)
		}
		w.call("setConfig", key, body.Value)
		rw.Header().Set("Content-Type", "application/json")
		if w.script.SetConfig.Success {
			_, _ = io.WriteString(rw, `{"ok":true}`)
			return
		}
		rw.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(rw).Encode(map[string]string{"error": w.script.SetConfig.Error})
	case r.Method == http.MethodGet && (r.URL.Path == "/api/health/detail" || r.URL.Path == "/api/health"):
		// One getHealth of the Node oracle: the detail call, and the short
		// one the client falls back to when the detail call fails
		if r.URL.Path == "/api/health/detail" {
			w.call("getHealth")
		}
		rw.Header().Set("Content-Type", "application/json")
		if w.script.Health != "running" {
			rw.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = io.WriteString(rw, `{"status":"ok"}`)
	default:
		w.t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
		http.NotFound(rw, r)
	}
}

func (w *oracleWorld) boot() {
	rollbacks := map[string]Rollback{}
	for _, kind := range []string{"connection-mode", "advanced-file", "billing-clock", "env-config"} {
		rollbacks[kind] = func(context.Context, *goapi.Object) error {
			w.call(kind)
			if message := w.script.RollbackErrors[kind]; message != "" {
				return errors.New(message)
			}
			return nil
		}
	}
	w.svc = New(Options{
		DataDir: w.dir,
		GoAPI:   goapi.New(http.HandlerFunc(w.api)),
		RestartServer: func(context.Context) (serverinfo.RestartResult, error) {
			w.call("restartServer")
			if w.script.Restart.Throw != nil {
				return serverinfo.RestartResult{}, errors.New(*w.script.Restart.Throw)
			}
			var result serverinfo.RestartResult
			if err := json.Unmarshal(w.script.Restart.Result, &result); err != nil {
				w.t.Fatalf("restart result of the vector: %v", err)
			}
			return result, nil
		},
		Rollbacks: rollbacks,
		Exit:      func() {},
		After: func(d time.Duration, _ func()) {
			if d != ExitDelay {
				w.t.Errorf("exit scheduled after %v, want %v", d, ExitDelay)
			}
			w.exit = true
		},
		Now:    w.now,
		Random: w,
		PID:    4242,
		Logger: quiet{},
	})
}

type quiet struct{}

func (quiet) Warn(...any) {}
func (quiet) Info(...any) {}

// run performs one step and returns the result as JSON, or the error.
func (w *oracleWorld) run(step vectorStep) (out *string, err error) {
	ctx := context.Background()
	text := func(v any, err error) (*string, error) {
		if err != nil {
			return nil, err
		}
		raw, marshalErr := httpx.Marshal(v)
		if marshalErr != nil {
			w.t.Fatal(marshalErr)
		}
		s := string(raw)
		return &s, nil
	}
	switch step.Op {
	case "boot":
		w.boot()
	case "rmdir":
		if err := os.RemoveAll(w.dir); err != nil {
			w.t.Fatal(err)
		}
	case "write":
		if err := os.MkdirAll(w.dir, 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.dir, stateFile), []byte(*step.Content), 0o600); err != nil {
			w.t.Fatal(err)
		}
	case "read":
		return text(w.svc.ReadState(), nil)
	case "pending":
		return text(w.svc.GetPending(step.User))
	case "register":
		change := Change{Key: step.Change.Key, Label: step.Change.Label}
		if rollback, parseErr := goapi.Parse(step.Change.Rollback); parseErr == nil {
			change.Rollback, _ = rollback.(*goapi.Object)
		}
		return text(w.svc.RegisterChange(step.User, change))
	case "cancel":
		return text(w.svc.Cancel(ctx, step.User, step.ID))
	case "confirm":
		return text(w.svc.Confirm(ctx, step.User, step.ID))
	case "status":
		return text(w.svc.GetPublicStatus(ctx, step.ID, step.Token))
	case "complete":
		return text(w.svc.Complete(step.User, step.ID))
	case "dismiss":
		return text(w.svc.DismissFailed(step.User, step.Key))
	default:
		w.t.Fatalf("unknown step %q", step.Op)
	}
	return nil, nil
}

func describe(s *string) string {
	if s == nil {
		return "(nothing)"
	}
	return *s
}

func TestNodeVectors(t *testing.T) {
	file := loadVectors(t)
	w := &oracleWorld{t: t, dir: filepath.Join(t.TempDir(), "data")}
	steps, failures := 0, 0
	for _, scenario := range file.Scenarios {
		for i, step := range scenario.Steps {
			steps++
			w.script, w.calls, w.exit = step.Script, nil, false
			out, err := w.run(step)

			var problems []string
			problem := func(what, got, want string) {
				problems = append(problems, what+":\n      go: "+got+"\n    node: "+want)
			}
			switch {
			case step.Err != nil && err == nil:
				problem("error", "none, result "+describe(out), step.Err.Message)
			case step.Err == nil && err != nil:
				problem("error", err.Error(), "none, result "+describe(step.Out))
			case step.Err != nil:
				if err.Error() != step.Err.Message {
					problem("error message", err.Error(), step.Err.Message)
				}
				var typed *Error
				status, details := 0, (*string)(nil)
				if errors.As(err, &typed) {
					status = typed.Status
					if typed.Details != nil {
						raw, _ := httpx.Marshal(typed.Details)
						s := string(raw)
						details = &s
					}
				}
				if status != step.Err.StatusCode {
					problem("error status", http.StatusText(status), http.StatusText(step.Err.StatusCode))
				}
				if describe(details) != describe(step.Err.Details) {
					problem("error details", describe(details), describe(step.Err.Details))
				}
				var typeErr *TypeError
				if errors.As(err, &typeErr) != (step.Err.Type == "TypeError") {
					problem("kind of error", err.Error(), step.Err.Type)
				}
			default:
				if describe(out) != describe(step.Out) {
					problem("result", describe(out), describe(step.Out))
				}
			}

			var have *string
			if raw, readErr := os.ReadFile(filepath.Join(w.dir, stateFile)); readErr == nil {
				s := string(raw)
				have = &s
			}
			if describe(have) != describe(step.File) {
				problem("file", describe(have), describe(step.File))
			}
			var calls []string
			for _, call := range step.Calls {
				calls = append(calls, string(call))
			}
			if strings.Join(w.calls, " ") != strings.Join(calls, " ") {
				problem("calls", strings.Join(w.calls, " "), strings.Join(calls, " "))
			}
			if w.exit != step.Exit {
				problem("exit scheduled", boolText(w.exit), boolText(step.Exit))
			}
			var dir []string
			if entries, readErr := os.ReadDir(w.dir); readErr == nil {
				dir = []string{}
				for _, entry := range entries {
					dir = append(dir, entry.Name())
				}
				sort.Strings(dir)
			}
			if strings.Join(dir, "|") != strings.Join(step.Dir, "|") || (dir == nil) != (step.Dir == nil) {
				problem("data folder", strings.Join(dir, "|"), strings.Join(step.Dir, "|"))
			}

			if len(problems) > 0 {
				failures++
				if failures <= 15 {
					spec, _ := json.Marshal(struct {
						Op, ID, Token, Key string
						User               int64
						Change             any
					}{step.Op, step.ID, step.Token, step.Key, step.User, step.Change})
					t.Errorf("%s, step %d %s\n  %s", scenario.Name, i, spec, strings.Join(problems, "\n  "))
				}
			}
		}
	}
	if failures > 15 {
		t.Errorf("%d of %d steps differ; the first 15 are shown", failures, steps)
	}
	if steps < 9000 {
		t.Errorf("replayed %d steps, the vector file should hold more than 9000", steps)
	}
}

func boolText(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
