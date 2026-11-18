package scheduledbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/backup"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// fakeBackups makes small archives with the real archive code, so the
// checks of a run are the real ones; it can be told to fail or to wait.
type fakeBackups struct {
	mu      sync.Mutex
	now     func() time.Time
	made    int
	fail    error
	corrupt bool
	hold    chan struct{}
	started chan struct{}
}

func (f *fakeBackups) CreateFullBackup(context.Context) ([]byte, string, error) {
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.hold != nil {
		<-f.hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, "", f.fail
	}
	f.made++
	manifest := `{"_format":"betterdesk-full-backup","_version":1,"components":["console/data.json"]}`
	if f.corrupt {
		manifest = `{"_format":"other","_version":1}`
	}
	archive, err := backup.CreateTarGz([]backup.Entry{{Name: "manifest.json", Data: []byte(manifest)}}, 1)
	if err != nil {
		return nil, "", err
	}
	name := "betterdesk-backup-" + f.now().UTC().Format("2006-01-02-15-04-05") + ".tar.gz"
	return archive, name, nil
}

func (f *fakeBackups) ValidateFullBackup(buf []byte) (backup.Validation, error) {
	return backup.New(backup.Options{}).ValidateFullBackup(buf)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestService(t *testing.T) (*Service, *fakeBackups, *clock, string) {
	t.Helper()
	db := storetest.Migrated(t)
	clk := &clock{t: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}
	fb := &fakeBackups{now: clk.now}
	data := t.TempDir()
	s := New(Options{Store: db, Backups: fb, DataDir: data, AppDir: "/app/console", Now: clk.now, PID: 42})
	return s, fb, clk, data
}

func status(t *testing.T, s *Service) *branding.Object {
	t.Helper()
	v, err := s.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v.(*branding.Object)
}

func TestRunWritesArchiveAndChecksum(t *testing.T) {
	s, _, _, data := newTestService(t)
	ctx := context.Background()
	res, err := s.CreateScheduledBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(data, "scheduled-backups")
	name := "betterdesk-backup-2026-11-10-10-00-00.tar.gz"
	if res.Filename != filepath.Join(dir, name) {
		t.Errorf("filename %q", res.Filename)
	}
	archive, err := os.ReadFile(res.Filename)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	if res.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 %s", res.SHA256)
	}
	side, err := os.ReadFile(res.Filename + ".sha256")
	if err != nil || string(side) != res.SHA256+"  "+name+"\n" {
		t.Errorf("checksum file %q %v", side, err)
	}
	for _, f := range []string{res.Filename, res.Filename + ".sha256"} {
		if info, _ := os.Stat(f); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o", f, info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("leftovers in the folder: %v", entries)
	}
	st := status(t, s)
	if got := branding.Stringify(st); got != `{"lastAttemptAt":"2026-11-10T10:00:00.000Z","lastSuccessAt":"2026-11-10T10:00:00.000Z","lastFile":"`+res.Filename+
		`","lastSha256":"`+res.SHA256+`","lastValidation":{"valid":true,"components":["console/data.json"],"sizeBytes":`+strconv.Itoa(len(archive))+`},"lastError":null,"lastPruned":0}` {
		t.Errorf("status %s", got)
	}
}

func TestRetentionKeepsTheNewest(t *testing.T) {
	s, _, clk, data := newTestService(t)
	ctx := context.Background()
	if _, err := s.SaveConfig(ctx, mustParse(t, `{"retentionCount":2}`)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(data, "scheduled-backups")
	var names []string
	for i := 0; i < 4; i++ {
		res, err := s.CreateScheduledBackup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// modification times in the order of the runs
		mod := clk.now()
		if err := os.Chtimes(res.Filename, mod, mod); err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(res.Filename))
		if i >= 2 && (len(res.Deleted) != 1 || res.Deleted[0] != names[i-2]) {
			t.Errorf("run %d deleted %q", i, res.Deleted)
		}
		clk.add(time.Hour)
	}
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := []string{names[2], names[2] + ".sha256", names[3], names[3] + ".sha256"}
	if strings.Join(left, ",") != strings.Join(want, ",") {
		t.Errorf("left %q, want %q", left, want)
	}
	// other files of the folder are never touched
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateScheduledBackup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("prune removed a file that is no archive")
	}
}

func TestFailedRunsAreRecorded(t *testing.T) {
	s, fb, _, data := newTestService(t)
	ctx := context.Background()

	fb.corrupt = true
	if _, err := s.CreateScheduledBackup(ctx); err == nil || err.Error() != "Generated backup failed validation: Archive is not a BetterDesk full backup (wrong _format)" {
		t.Errorf("corrupt: %v", err)
	}
	st := branding.Stringify(status(t, s))
	if !strings.Contains(st, `"lastValidation":{"valid":false},"lastError":"Generated backup failed validation`) {
		t.Errorf("status %s", st)
	}
	fb.corrupt = false

	fb.fail = errors.New("dump failed")
	if _, err := s.CreateScheduledBackup(ctx); err == nil || err.Error() != "dump failed" {
		t.Errorf("fail: %v", err)
	}
	fb.fail = nil

	// a destination that cannot be made
	blocker := filepath.Join(data, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveConfig(ctx, mustParse(t, `{"destinationType":"path","destinationPath":"`+filepath.Join(blocker, "sub")+`"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateScheduledBackup(ctx)
	want := "ENOTDIR: not a directory, mkdir '" + filepath.Join(blocker, "sub") + "'"
	if err == nil || err.Error() != want {
		t.Errorf("unwritable: %v, want %s", err, want)
	}
	if !strings.Contains(branding.Stringify(status(t, s)), `"lastError":"`+want+`"`) {
		t.Errorf("status %s", branding.Stringify(status(t, s)))
	}
	// the service is free again after every failure
	if _, err := s.SaveConfig(ctx, mustParse(t, `{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateScheduledBackup(ctx); err != nil {
		t.Errorf("after the failures: %v", err)
	}
}

func TestOneRunAtATime(t *testing.T) {
	s, fb, _, _ := newTestService(t)
	ctx := context.Background()
	fb.hold = make(chan struct{})
	fb.started = make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateScheduledBackup(ctx)
		done <- err
	}()
	<-fb.started
	second := make(chan error, 1)
	go func() {
		_, err := s.CreateScheduledBackup(ctx)
		second <- err
	}()
	select {
	case err := <-second:
		if !errors.Is(err, ErrRunning) || err.Error() != "A scheduled backup is already running" {
			t.Errorf("second run: %v", err)
		}
	case <-time.After(5 * time.Second):
		// it started a second backup and waits behind the first
		t.Fatal("a second run started while the first one was going on")
	}
	if skip, _, err := s.RunIfDue(ctx); err != nil || skip == nil || skip.Reason != "running" {
		t.Errorf("due check while running: %+v %v", skip, err)
	}
	fb.started = nil
	close(fb.hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateScheduledBackup(ctx); err != nil {
		t.Errorf("after the run: %v", err)
	}
}

func TestRunIfDue(t *testing.T) {
	s, fb, clk, _ := newTestService(t)
	ctx := context.Background()
	if skip, _, _ := s.RunIfDue(ctx); skip == nil || skip.Reason != "disabled" {
		t.Errorf("disabled: %+v", skip)
	}
	if _, err := s.SaveConfig(ctx, mustParse(t, `{"enabled":true,"intervalHours":2}`)); err != nil {
		t.Fatal(err)
	}
	if skip, res, err := s.RunIfDue(ctx); skip != nil || res == nil || err != nil {
		t.Fatalf("first run: %+v %v", skip, err)
	}
	clk.add(2*time.Hour - time.Millisecond)
	if skip, _, _ := s.RunIfDue(ctx); skip == nil || skip.Reason != "not_due" {
		t.Errorf("before the interval: %+v", skip)
	}
	clk.add(time.Millisecond)
	if skip, res, _ := s.RunIfDue(ctx); skip != nil || res == nil {
		t.Errorf("at the interval: %+v", skip)
	}
	if fb.made != 2 {
		t.Errorf("%d archives", fb.made)
	}
	// a failed attempt counts as an attempt
	fb.fail = errors.New("x")
	clk.add(2 * time.Hour)
	if _, _, err := s.RunIfDue(ctx); err == nil {
		t.Error("the failing run did not report")
	}
	fb.fail = nil
	clk.add(time.Hour)
	if skip, _, _ := s.RunIfDue(ctx); skip == nil || skip.Reason != "not_due" {
		t.Errorf("after a failed attempt: %+v", skip)
	}
}

// The job checks once a minute; it is driven here by a ticker the test
// owns, so nothing waits for a real minute.
func TestJobRunsOnItsTicker(t *testing.T) {
	s, fb, _, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	var interval time.Duration
	s.o.Ticker = func(d time.Duration) (<-chan time.Time, func()) {
		interval = d
		return tick, func() {}
	}
	if _, err := s.SaveConfig(context.Background(), mustParse(t, `{"enabled":true,"intervalHours":1}`)); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	tick <- time.Now()
	tick <- time.Now() // the second tick waits until the first check is through
	cancel()
	<-done
	if interval != time.Minute {
		t.Errorf("interval %v", interval)
	}
	if fb.made != 1 {
		t.Errorf("%d runs, want 1 (the second tick is not due)", fb.made)
	}
}

func TestDestinationProbe(t *testing.T) {
	s, _, _, data := newTestService(t)
	dir := filepath.Join(data, "probe", "deep")
	got, err := s.TestDestination(mustParse(t, `{"destinationType":"path","destinationPath":" `+dir+` "}`))
	if err != nil || got != dir {
		t.Fatalf("%q %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the probe stayed: %v", entries)
	}
	if _, err := s.TestDestination(mustParse(t, `{"destinationType":"path","destinationPath":"/app/console/sub"}`)); err == nil ||
		err.Error() != "The BetterDesk application directory cannot be used as a backup destination" {
		t.Errorf("app folder: %v", err)
	}
}

// A folder that cannot be made answers at once. Node's recursive mkdir
// looped for ever below /proc (BUGS.md BK15).
func TestDestinationBelowProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	s, _, _, _ := newTestService(t)
	done := make(chan error, 1)
	go func() {
		_, err := s.TestDestination(mustParse(t, `{"destinationType":"path","destinationPath":"/proc/bk-test"}`))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "ENOENT: no such file or directory, mkdir '/proc/bk-test'" {
			t.Errorf("err %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no answer")
	}
}

func TestSMBPasswordIsNeverAnswered(t *testing.T) {
	s, _, _, _ := newTestService(t)
	ctx := context.Background()
	saved, err := s.SaveSMB(ctx, mustParse(t, `{"host":"nas","share":"b","username":"u","password":"hunter2"}`))
	if err != nil || strings.Contains(saved, "hunter2") || !strings.Contains(saved, `"password":"********"`) {
		t.Fatalf("save answered %s %v", saved, err)
	}
	got, err := s.GetSMB(ctx)
	if err != nil || strings.Contains(got, "hunter2") || got != `{"host":"nas","share":"b","domain":"","username":"u","password":"********","mountPath":""}` {
		t.Errorf("get answered %s %v", got, err)
	}
	// the mask keeps the stored password; the test then uses it
	if _, err := s.SaveSMB(ctx, mustParse(t, `{"host":"nas","share":"b","username":"u","password":"********"}`)); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.getSetting(ctx, SMBKey)
	if !strings.Contains(stored.String, `"password":"hunter2"`) {
		t.Errorf("stored %s", stored.String)
	}
	var args []string
	s.o.Run = func(_ context.Context, name string, a ...string) (int, error) { args = a; return 0, nil }
	f := false
	s.o.Windows = &f
	target, err := s.TestSMB(ctx, mustParse(t, `{"host":"nas","share":"b","username":"u","password":"********"}`))
	if err != nil || target != "//nas/b" || strings.Join(args, " ") != "//nas/b -U u%hunter2 -c dir" {
		t.Errorf("test: %q %v %q", target, err, args)
	}
	// an empty password clears it
	if out, _ := s.SaveSMB(ctx, mustParse(t, `{"host":"nas","share":"b","password":""}`)); !strings.Contains(out, `"password":""`) {
		t.Errorf("cleared: %s", out)
	}
}

func TestSMBTimeout(t *testing.T) {
	s, _, _, _ := newTestService(t)
	s.o.SMBTimeout = 20 * time.Millisecond
	f := false
	s.o.Windows = &f
	s.o.Run = func(ctx context.Context, _ string, _ ...string) (int, error) {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	if _, err := s.TestSMBConnection(context.Background(), mustParse(t, `{"host":"nas","share":"b"}`)); err == nil || err.Error() != "SMB connection test timed out" {
		t.Errorf("timeout: %v", err)
	}
}

func mustParse(t *testing.T, text string) any {
	t.Helper()
	v, err := branding.ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
