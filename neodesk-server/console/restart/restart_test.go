package restart

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

func goConfig(key, value string) *goapi.Object {
	return goapi.NewObject("type", "go-config", "key", key, "value", value)
}

func okRestart(context.Context) (serverinfo.RestartResult, error) {
	return serverinfo.RestartResult{
		Source:   "docker",
		Restarts: []*goapi.Object{goapi.NewObject("component", "server", "service", "neodesk-server", "success", true)},
	}, nil
}

func newService(t *testing.T, o Options) *Service {
	t.Helper()
	if o.DataDir == "" {
		o.DataDir = filepath.Join(t.TempDir(), "data")
	}
	if o.Logger == nil {
		o.Logger = quiet{}
	}
	return New(o)
}

func mustRegister(t *testing.T, s *Service, user int64, key string) *Public {
	t.Helper()
	public, err := s.RegisterChange(user, Change{Key: key, Label: strings.ToUpper(key), Rollback: goConfig(key, "N")})
	if err != nil {
		t.Fatalf("register %s: %v", key, err)
	}
	return public
}

func TestStateFileIsPrivateAndWhole(t *testing.T) {
	s := newService(t, Options{PID: 77})
	public := mustRegister(t, s, 5, "cdap-enabled")

	if got, want := s.StatePath(), filepath.Join(s.opts.DataDir, ".restart-transaction.json"); got != want {
		t.Fatalf("state path %q, want %q", got, want)
	}
	info, err := os.Stat(s.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the transaction file has mode %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(s.StatePath() + ".tmp-77"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temporary file is still there: %v", err)
	}
	raw, err := os.ReadFile(s.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		ID, UserID, Phase string
		Changes           []struct {
			Key, Label string
			Rollback   map[string]string
		}
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("the file is no JSON: %v\n%s", err, raw)
	}
	if state.ID != public.ID() || state.UserID != "5" || state.Phase != "pending" || len(state.Changes) != 1 ||
		state.Changes[0].Rollback["type"] != "go-config" || state.Changes[0].Label != "CDAP-ENABLED" {
		t.Errorf("the file holds %+v", state)
	}
	if !strings.HasPrefix(string(raw), "{\n  \"id\": \"") {
		t.Errorf("the file is not indented by two spaces:\n%s", raw)
	}
	if len(public.ID()) != 36 || public.ID()[14] != '4' || !strings.ContainsRune("89ab", rune(public.ID()[19])) {
		t.Errorf("the id %q is no version 4 UUID", public.ID())
	}
}

func TestExitAfterAConfirmedRestart(t *testing.T) {
	var delay time.Duration
	var scheduled func()
	exits := 0
	s := newService(t, Options{
		RestartServer: okRestart,
		Exit:          func() { exits++ },
		After:         func(d time.Duration, f func()) { delay, scheduled = d, f },
	})
	public := mustRegister(t, s, 1, "k")
	confirmed, err := s.Confirm(context.Background(), 1, public.ID())
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase() != PhaseRestarting || len(confirmed.Token()) != 48 {
		t.Errorf("confirmed: phase %q, token of %d characters", confirmed.Phase(), len(confirmed.Token()))
	}
	if scheduled == nil || delay != 1500*time.Millisecond {
		t.Fatalf("exit scheduled: %v after %v, want after 1.5s", scheduled != nil, delay)
	}
	if exits != 0 {
		t.Fatal("the process ended before the delay")
	}
	scheduled()
	if exits != 1 {
		t.Errorf("Exit ran %d times, want once", exits)
	}
}

func TestNoExitWithoutAnExitFunction(t *testing.T) {
	scheduled := false
	s := newService(t, Options{RestartServer: okRestart, After: func(time.Duration, func()) { scheduled = true }})
	public := mustRegister(t, s, 1, "k")
	if _, err := s.Confirm(context.Background(), 1, public.ID()); err != nil {
		t.Fatal(err)
	}
	if scheduled {
		t.Error("an exit was scheduled although Options.Exit is nil")
	}
}

func TestNoExitAfterAFailedRestart(t *testing.T) {
	scheduled := false
	api := goapi.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	s := newService(t, Options{
		GoAPI: api,
		RestartServer: func(context.Context) (serverinfo.RestartResult, error) {
			return serverinfo.RestartResult{Source: "docker", Restarts: []*goapi.Object{goapi.NewObject("success", false, "error", "docker: not found")}}, nil
		},
		Exit:  func() {},
		After: func(time.Duration, func()) { scheduled = true },
	})
	public := mustRegister(t, s, 1, "k")
	confirmed, err := s.Confirm(context.Background(), 1, public.ID())
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase() != PhaseFailed || confirmed.Get("error") != "service_restart_failed_rolled_back" {
		t.Errorf("phase %q, error %v", confirmed.Phase(), confirmed.Get("error"))
	}
	if scheduled {
		t.Error("an exit was scheduled after a failed restart")
	}
}

func TestConfirmWithoutARestartFunction(t *testing.T) {
	s := newService(t, Options{})
	public := mustRegister(t, s, 1, "k")
	_, err := s.Confirm(context.Background(), 1, public.ID())
	if !errors.Is(err, serverinfo.ErrNoRestart) || err.Error() != "no_restart" {
		t.Fatalf("error %v, want no_restart", err)
	}
	pending, err := s.GetPending(1)
	if err != nil {
		t.Fatal(err)
	}
	// the go-config rollback has no API to call
	if pending.Phase() != PhaseFailed || pending.Get("error") != "service_restart_failed_rollback_failed" {
		t.Errorf("after the refused restart: phase %q, error %v", pending.Phase(), pending.Get("error"))
	}
}

// TestRegisterChangeAfterWritesNothingWhenRefused: a change that the
// transaction refuses never runs its write, a write that fails registers
// nothing, and a write that works is registered under the key (BUGS SE2,
// CM5).
func TestRegisterChangeAfterWritesNothingWhenRefused(t *testing.T) {
	s := newService(t, Options{})
	mustRegister(t, s, 1, "first")
	wrote := 0
	write := func(err error) func() (Change, error) {
		return func() (Change, error) {
			wrote++
			return Change{Key: "ignored", Label: "File", Rollback: goConfig("file", "N")}, err
		}
	}
	_, err := s.RegisterChangeAfter(2, "file", write(nil))
	var refused *Error
	if !errors.As(err, &refused) || refused.StatusCode() != http.StatusConflict || refused.Message != "restart_transaction_owned" || wrote != 0 {
		t.Errorf("another operator: %v, wrote %d times", err, wrote)
	}
	if _, err := s.RegisterChangeAfter(0, "file", write(nil)); err == nil || err.Error() != "restart_auth_required" || wrote != 0 {
		t.Errorf("nobody signed in: %v, wrote %d times", err, wrote)
	}
	failed := errors.New("disk full")
	if _, err := s.RegisterChangeAfter(1, "file", write(failed)); !errors.Is(err, failed) || wrote != 1 {
		t.Errorf("a failed write: %v, wrote %d times", err, wrote)
	}
	public, err := s.RegisterChangeAfter(1, "file", write(nil))
	if err != nil || wrote != 2 {
		t.Fatalf("a write that works: %v, wrote %d times", err, wrote)
	}
	if text, _ := json.Marshal(public.Get("changes")); string(text) != `[{"key":"first","label":"FIRST"},{"key":"file","label":"File"}]` {
		t.Errorf("changes %s", text)
	}
}

// TestRegisterChangeWaitsForARunningRestart: while a transaction is
// restarting under this process's boot id, a new change is refused for
// every operator and the transaction stays, so its page can follow it.
// After the restart, in a process with another boot id, it is replaced
// (BUGS RC3).
func TestRegisterChangeWaitsForARunningRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s := newService(t, Options{DataDir: dir, RestartServer: okRestart})
	public := mustRegister(t, s, 1, "k")
	confirmed, err := s.Confirm(context.Background(), 1, public.ID())
	if err != nil || confirmed.Phase() != PhaseRestarting {
		t.Fatalf("confirm: %v", err)
	}
	before, _ := os.ReadFile(s.StatePath())
	for _, user := range []int64{2, 1} {
		_, err := s.RegisterChange(user, Change{Key: "other", Rollback: goConfig("other", "N")})
		var refused *Error
		if !errors.As(err, &refused) || refused.StatusCode() != http.StatusConflict || refused.Message != "restart_transaction_owned" {
			t.Errorf("operator %d during the restart: %v", user, err)
		}
	}
	if after, _ := os.ReadFile(s.StatePath()); string(after) != string(before) {
		t.Errorf("the restarting transaction changed:\n%s\nwas\n%s", after, before)
	}
	next := newService(t, Options{DataDir: dir})
	replaced, err := next.RegisterChange(2, Change{Key: "other", Rollback: goConfig("other", "N")})
	if err != nil || replaced.Phase() != PhasePending || replaced.ID() == public.ID() {
		t.Errorf("after the restart: %v, %v", replaced, err)
	}
}

// TestConfirmRollsBackWhenTheRestartFails: a restart that fails with an
// error undoes the changes as one that reports success false does, and the
// transaction says whether everything came back; the answer is still the
// restart's error (BUGS RC2).
func TestConfirmRollsBackWhenTheRestartFails(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rollbackErr error
		want        string
	}{
		{"undone", nil, "service_restart_failed_rolled_back"},
		{"not undone", errors.New("read-only"), "service_restart_failed_rollback_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newService(t, Options{
				RestartServer: func(context.Context) (serverinfo.RestartResult, error) {
					return serverinfo.RestartResult{}, serverinfo.ErrNoRestart
				},
				Exit:  func() {},
				After: func(time.Duration, func()) { t.Error("an exit was scheduled after a failed restart") },
			})
			var undone []string
			s.SetRollback("advanced-file", func(_ context.Context, descriptor *goapi.Object) error {
				id, _ := descriptor.Get("fileId").(string)
				undone = append(undone, id)
				return tc.rollbackErr
			})
			var public *Public
			for _, id := range []string{"server-env", "console-env"} {
				var err error
				public, err = s.RegisterChange(1, Change{Key: id, Rollback: goapi.NewObject("type", "advanced-file", "fileId", id)})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Confirm(context.Background(), 1, public.ID()); !errors.Is(err, serverinfo.ErrNoRestart) {
				t.Fatalf("confirm: %v, want no_restart", err)
			}
			if strings.Join(undone, ",") != "console-env,server-env" {
				t.Errorf("undone: %v, want both changes, newest first", undone)
			}
			pending, err := s.GetPending(1)
			if err != nil {
				t.Fatal(err)
			}
			if pending.Phase() != PhaseFailed || pending.Get("error") != tc.want {
				t.Errorf("after the failed restart: phase %q, error %v; want failed, %s", pending.Phase(), pending.Get("error"), tc.want)
			}
		})
	}
}

func TestErrorStatusCode(t *testing.T) {
	s := newService(t, Options{})
	_, err := s.Complete(1, "nothing")
	var refused *Error
	if !errors.As(err, &refused) || refused.StatusCode() != 404 || refused.Message != "restart_transaction_not_found" {
		t.Fatalf("complete without a transaction: %v", err)
	}
	if code := (&Error{Message: "restart_auth_required"}).StatusCode(); code != 500 {
		t.Errorf("an error without a status answers %d, want 500", code)
	}
	mustRegister(t, s, 1, "k")
	if _, err := s.RegisterChange(2, Change{Key: "k", Rollback: goConfig("k", "N")}); !errors.As(err, &refused) || refused.StatusCode() != 409 {
		t.Errorf("a second operator: %v", err)
	}
}

func TestSetRollback(t *testing.T) {
	s := newService(t, Options{})
	var got string
	s.SetRollback("advanced-file", func(_ context.Context, descriptor *goapi.Object) error {
		got, _ = descriptor.Get("fileId").(string)
		return nil
	})
	public, err := s.RegisterChange(1, Change{Key: "file", Rollback: goapi.NewObject("type", "advanced-file", "fileId", "server-env")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(context.Background(), 1, public.ID()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got != "server-env" {
		t.Errorf("the rollback got the file id %q", got)
	}
	if s.ReadState() != nil {
		t.Error("the transaction is still there after the cancel")
	}

	// Without the function the kind is unknown and the cancel fails
	fresh := newService(t, Options{})
	public, err = fresh.RegisterChange(1, Change{Key: "file", Rollback: goapi.NewObject("type", "advanced-file")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fresh.Cancel(context.Background(), 1, public.ID())
	var refused *Error
	if !errors.As(err, &refused) || refused.Message != "restart_rollback_failed" || len(refused.Details) != 1 {
		t.Fatalf("cancel without the rollback function: %v", err)
	}
	if failure := refused.Details[0].(*goapi.Object); failure.Get("error") != "unknown_restart_rollback" || failure.Get("key") != "file" {
		t.Errorf("failure %v", failure)
	}
}

func TestConnectionModeRollback(t *testing.T) {
	dir := t.TempDir()
	info := serverinfo.New(serverinfo.Options{
		SystemdUnit:   filepath.Join(dir, "no.service"),
		ComposePath:   filepath.Join(dir, "docker-compose.yml"),
		DockerEnvFile: filepath.Join(dir, "no-dockerenv"),
		Getenv:        func(string) string { return "" },
	})
	rollback := ConnectionModeRollback(info)
	ctx := context.Background()

	err := rollback(ctx, goapi.NewObject("type", "connection-mode"))
	var typeErr *TypeError
	if !errors.As(err, &typeErr) || err.Error() != "Cannot read properties of undefined (reading 'mode')" {
		t.Errorf("a descriptor without settings: %v", err)
	}
	if err := rollback(ctx, goapi.NewObject("settings", nil)); err == nil || err.Error() != "Cannot read properties of null (reading 'mode')" {
		t.Errorf("a descriptor with null settings: %v", err)
	}
	// Neither a unit nor a compose file: nowhere to store the mode
	if err := rollback(ctx, goapi.NewObject("settings", goapi.NewObject("mode", "relay_only"))); !errors.Is(err, serverinfo.ErrNotConfigurable) {
		t.Errorf("without a place to store the mode: %v", err)
	}

	compose := "services:\n  server:\n    environment:\n      - P2P_FIRST=Y\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := goapi.NewObject("mode", "relay_only", "p2p_fallback_ms", float64(1500), "same_nat_relay", false)
	if err := rollback(ctx, goapi.NewObject("type", "connection-mode", "settings", settings)); err != nil {
		t.Fatalf("rollback into the compose file: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ALWAYS_USE_RELAY=Y", "P2P_FALLBACK_MS=1500", "SAME_NAT_RELAY=N"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the compose file lacks %s:\n%s", want, written)
		}
	}
}

func TestPublicStatusNeedsTheAPIAfterAReboot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first := newService(t, Options{DataDir: dir, RestartServer: okRestart})
	public := mustRegister(t, first, 1, "k")
	confirmed, err := first.Confirm(context.Background(), 1, public.ID())
	if err != nil {
		t.Fatal(err)
	}
	// The process that confirmed keeps saying "restarting" and asks nobody
	status, err := first.GetPublicStatus(context.Background(), confirmed.ID(), confirmed.Token())
	if err != nil || status.Phase() != PhaseRestarting {
		t.Fatalf("same boot: %v, %v", status.Phase(), err)
	}
	second := newService(t, Options{DataDir: dir})
	if second.BootID() == first.BootID() || len(second.BootID()) != 32 {
		t.Fatalf("boot ids %q and %q", first.BootID(), second.BootID())
	}
	if _, err := second.GetPublicStatus(context.Background(), confirmed.ID(), confirmed.Token()); err == nil {
		t.Error("a new process without an API client answered a restarting transaction")
	}
	unknown, err := second.GetPublicStatus(context.Background(), confirmed.ID(), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(unknown); string(raw) != `{"phase":"unknown","status":"unknown","ready":false}` {
		t.Errorf("a wrong token gets %s", raw)
	}
}

func TestNilPublicIsNull(t *testing.T) {
	s := newService(t, Options{})
	pending, err := s.GetPending(1)
	if err != nil || pending != nil {
		t.Fatalf("pending without a transaction: %v, %v", pending, err)
	}
	raw, err := json.Marshal(struct {
		Data *Public `json:"data"`
	}{pending})
	if err != nil || string(raw) != `{"data":null}` {
		t.Errorf("encoded as %s, %v", raw, err)
	}
	if pending.ID() != "" || pending.Phase() != "" || pending.Get("id") != nil {
		t.Error("a nil transaction has values")
	}
}

// TestConcurrentCalls runs the calls of several requests at once. With
// -race it shows unguarded state; without, it still checks that the file
// stays one whole transaction.
func TestConcurrentCalls(t *testing.T) {
	s := newService(t, Options{RestartServer: okRestart})
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				key := string(rune('a' + (worker+i)%20))
				_, _ = s.RegisterChange(1, Change{Key: key, Rollback: goConfig(key, "N")})
				_, _ = s.GetPending(1)
				_, _ = s.DismissFailed(2, "")
				_, _ = s.GetPublicStatus(context.Background(), "x", "y")
				s.SetRollback("billing-clock", func(context.Context, *goapi.Object) error { return nil })
				_ = s.ReadState()
			}
		}()
	}
	wg.Wait()
	state, isObject := s.ReadState().(*goapi.Object)
	if !isObject {
		t.Fatal("no transaction after the run")
	}
	changes, _ := state.Get("changes").([]any)
	if len(changes) != 20 {
		t.Errorf("%d changes, want one per key (20)", len(changes))
	}
	entries, err := os.ReadDir(s.opts.DataDir)
	if err != nil || len(entries) != 1 {
		t.Errorf("the data folder holds %d entries, want the transaction file alone (%v)", len(entries), err)
	}
}

// FuzzStateFile puts arbitrary bytes in place of the transaction file and
// makes every call. No content may stop the console, and a file the
// service writes afterwards is JSON again.
func FuzzStateFile(f *testing.F) {
	seeds := []string{
		"", "null", "[]", "{}", "{\"a\":", "\xff\xfe", `{"id":"x","userId":"1","phase":"pending","changes":[]}`,
		`{"id":"x","userId":"1","phase":"pending","changes":[null]}`, `{"id":"x","userId":"1","phase":"pending","changes":"abc"}`,
		`{"id":"x","userId":"1","phase":"failed","changes":[{"key":"k","rollback":{"type":"go-config","key":"k","value":1}}]}`,
		`{"id":"x","userId":"1","phase":"restarting","token":"t","bootId":"b","changes":{"length":1}}`,
		`{"id":"x","userId":"1","phase":"ready","changes":[5,"a",[],{"key":5}]}`,
		`{"id":"x","userId":"1","phase":"pending","changes":[{"key":"k","rollback":{"type":"connection-mode"}},{"rollback":"text"}]}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}
	api := goapi.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	f.Fuzz(func(t *testing.T, content []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, stateFile)
		ctx := context.Background()
		calls := []func(s *Service){
			func(s *Service) { _, _ = s.GetPending(1) },
			func(s *Service) { _, _ = s.RegisterChange(1, Change{Key: "k", Rollback: goConfig("k", "N")}) },
			func(s *Service) { _, _ = s.Cancel(ctx, 1, "x") },
			func(s *Service) { _, _ = s.Confirm(ctx, 1, "x") },
			func(s *Service) { _, _ = s.GetPublicStatus(ctx, "x", "t") },
			func(s *Service) { _, _ = s.Complete(1, "x") },
			func(s *Service) { _, _ = s.DismissFailed(1, "k") },
			func(s *Service) { _ = s.ReadState() },
		}
		for _, call := range calls {
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			s := New(Options{DataDir: dir, GoAPI: api, RestartServer: okRestart, Logger: quiet{}, Rollbacks: map[string]Rollback{
				"connection-mode": func(context.Context, *goapi.Object) error { return errors.New("no") },
			}})
			call(s)
			after, err := os.ReadFile(path)
			if err != nil || string(after) == string(content) {
				continue
			}
			if _, err := goapi.Parse(after); err != nil {
				t.Fatalf("the service wrote a file that is no JSON: %v\n%s", err, after)
			}
		}
	})
}

// TestNegativeZeroIsWrittenAsZero pins how a number is written into the
// file and into an answer: JSON.stringify knows no negative zero.
func TestNegativeZeroIsWrittenAsZero(t *testing.T) {
	s := newService(t, Options{})
	negativeZero, err := goapi.Parse([]byte(`{"type":"go-config","key":"k","value":{"n":-0.0,"list":[-0]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterChange(1, Change{Key: "k", Rollback: negativeZero.(*goapi.Object)}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	// only the values the test wrote: the id and the times may hold "-0"
	var state struct {
		Changes []struct {
			Rollback struct {
				Value struct {
					N    json.Number   `json:"n"`
					List []json.Number `json:"list"`
				} `json:"value"`
			} `json:"rollback"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &state); err != nil || len(state.Changes) != 1 {
		t.Fatalf("the file: %v\n%s", err, raw)
	}
	if value := state.Changes[0].Rollback.Value; value.N != "0" || len(value.List) != 1 || value.List[0] != "0" {
		t.Errorf("the file holds a negative zero:\n%s", raw)
	}
	public := &Public{fields: goapi.NewObject("id", negativeZero.(*goapi.Object).Get("value"))}
	if text, _ := json.Marshal(public); string(text) != `{"id":{"n":0,"list":[0]}}` {
		t.Errorf("an answer holds %s", text)
	}
}

// The id and the token of a transaction are compared as text of the same
// bytes, neither of them empty (tokensEqual of upstream 3.5.264, audit P3):
// a transaction file with an empty id and token answers nothing to a
// caller who sends empty ones, and text of another length or another type
// matches nothing.
func TestPublicStatusComparesIDAndToken(t *testing.T) {
	for _, c := range []struct {
		expected any
		actual   string
		want     bool
	}{
		{"abc", "abc", true}, {"", "", false}, {"abc", "", false}, {"", "abc", false},
		{"abc", "abd", false}, {"abc", "abcd", false}, {"é", "é", true}, {float64(1), "1", false}, {nil, "", false},
	} {
		if got := tokensEqual(c.expected, c.actual); got != c.want {
			t.Errorf("tokensEqual(%#v, %q) = %v, want %v", c.expected, c.actual, got, c.want)
		}
	}

	dir := filepath.Join(t.TempDir(), "data")
	s := newService(t, Options{DataDir: dir, RestartServer: okRestart})
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"id":"","token":"","userId":"1","phase":"pending","createdAt":"2026-11-10T08:00:00.000Z","updatedAt":"2026-11-10T08:00:00.000Z","changes":[]}`
	if err := os.WriteFile(s.StatePath(), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := s.GetPublicStatus(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(status); string(raw) != `{"phase":"unknown","status":"unknown","ready":false}` {
		t.Errorf("an empty id and token got %s", raw)
	}
}
