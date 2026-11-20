package filetransfer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

func newService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s := New(Options{Dir: filepath.Join(t.TempDir(), "data", "transfers"), Now: func() time.Time { return now }, Logger: quiet{}})
	return s, &now
}

func TestNewCreatesTheFolder(t *testing.T) {
	s, _ := newService(t)
	info, err := os.Stat(s.opts.Dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("the transfers folder was not created: %v", err)
	}
}

func TestAFileStaysInsideTheFolder(t *testing.T) {
	s, _ := newService(t)
	for _, name := range []string{"../../etc/passwd", "/etc/shadow", "..", "a/../../b", `..\..\windows\system.ini`, "x/\x00y"} {
		record, err := s.CreateTransfer(Params{Filename: name, DeviceID: "d"})
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if filepath.Dir(record.TempPath) != s.opts.Dir {
			t.Errorf("%q: the file %q is not directly in %q", name, record.TempPath, s.opts.Dir)
		}
		if base := filepath.Base(record.TempPath); !strings.HasPrefix(base, record.ID+"_") || strings.ContainsAny(base, `/\`) {
			t.Errorf("%q: file name %q", name, base)
		}
	}
}

func TestChunksCompleteATransfer(t *testing.T) {
	s, _ := newService(t)
	record, err := s.CreateTransfer(Params{Filename: "note.txt", Size: float64(11), DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if s.GetCompletedFilePath(record.ID) != "" {
		t.Error("a pending transfer has a file to download")
	}
	first, err := s.ReceiveChunk(record.ID, float64(0), base64.StdEncoding.EncodeToString([]byte("hello ")))
	if err != nil || first.Status != StatusActive || first.BytesTransferred != 6 {
		t.Fatalf("first chunk: %+v, %v", first, err)
	}
	last, err := s.ReceiveChunk(record.ID, float64(1), base64.StdEncoding.EncodeToString([]byte("world")))
	if err != nil || last.Status != StatusCompleted || last.Percent != 100 {
		t.Fatalf("last chunk: %+v, %v", last, err)
	}
	path := s.GetCompletedFilePath(record.ID)
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "hello world" {
		t.Fatalf("the file holds %q, %v", content, err)
	}
	if !s.CancelTransfer(record.ID) {
		t.Fatal("cancel found no transfer")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the file is still there after the cancel: %v", err)
	}
	if _, err := s.ReceiveChunk(record.ID, float64(2), "QQ=="); err == nil || err.Error() != "Transfer is cancelled" {
		t.Errorf("a chunk for a cancelled transfer: %v", err)
	}
}

func TestSweep(t *testing.T) {
	s, now := newService(t)
	create := func(name string, size float64) string {
		record, err := s.CreateTransfer(Params{Filename: name, Size: size, DeviceID: "d"})
		if err != nil {
			t.Fatal(err)
		}
		return record.ID
	}
	pending, active, done := create("pending", 5), create("active", 50), create("done", 1)
	for _, id := range []string{active, done} {
		if _, err := s.ReceiveChunk(id, float64(0), "QQ=="); err != nil {
			t.Fatal(err)
		}
	}
	activeFile, doneFile := s.GetTransfer(active).TempPath, s.GetTransfer(done).TempPath

	*now = now.Add(TransferTimeout)
	s.Sweep()
	if got := s.GetTransfer(active).Status; got != StatusActive {
		t.Fatalf("after exactly 30 minutes the active transfer is %s", got)
	}
	*now = now.Add(time.Millisecond)
	s.Sweep()
	if got := s.GetTransfer(active).Status; got != StatusFailed {
		t.Fatalf("after 30 minutes and a millisecond the active transfer is %s", got)
	}
	if _, err := os.Stat(activeFile); !os.IsNotExist(err) {
		t.Errorf("the file of the failed transfer is still there: %v", err)
	}

	*now = now.Add(Retention - TransferTimeout - time.Millisecond)
	s.Sweep()
	if s.GetTransfer(done) == nil || s.GetTransfer(active) == nil {
		t.Fatal("a finished transfer is gone after exactly an hour")
	}
	*now = now.Add(time.Millisecond)
	s.Sweep()
	if s.GetTransfer(done) != nil || s.GetTransfer(active) != nil {
		t.Error("finished transfers are still listed after more than an hour")
	}
	if _, err := os.Stat(doneFile); err != nil {
		t.Errorf("the file of the completed transfer was removed with its record: %v", err)
	}
	if s.GetTransfer(pending) == nil {
		t.Error("the pending transfer was swept")
	}
	if list := s.ListTransfers(Filter{}); len(list) != 1 || list[0].ID != pending {
		t.Errorf("the list holds %d transfers", len(list))
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	s, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

// repeating gives the same bytes for every id.
type repeating struct{}

func (repeating) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0xab
	}
	return len(p), nil
}

func TestAnIDThatIsTakenReplacesTheTransfer(t *testing.T) {
	s := New(Options{Dir: t.TempDir(), Random: repeating{}, Logger: quiet{}})
	if _, err := s.CreateTransfer(Params{Filename: "one", DeviceID: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTransfer(Params{Filename: "two", DeviceID: "d"}); err != nil {
		t.Fatal(err)
	}
	list := s.ListTransfers(Filter{})
	if len(list) != 1 || list[0].Filename != "two" {
		t.Errorf("the list holds %d transfers, the first named %q", len(list), list[0].Filename)
	}
}

func TestRecordsAreCopies(t *testing.T) {
	s, _ := newService(t)
	record, _ := s.CreateTransfer(Params{Filename: "a", DeviceID: "d"})
	record.Status = StatusCompleted
	s.GetTransfer(record.ID).Status = StatusCompleted
	s.ListTransfers(Filter{})[0].Status = StatusCompleted
	if got := s.GetTransfer(record.ID).Status; got != StatusPending {
		t.Errorf("a caller changed the registry: status %s", got)
	}
}

func TestMarshalledShapes(t *testing.T) {
	s, _ := newService(t)
	record, _ := s.CreateTransfer(Params{Filename: "a.txt", DeviceID: Undefined, Size: "abc"})
	raw, _ := json.Marshal(record)
	if strings.Contains(string(raw), "deviceId") || !strings.HasPrefix(string(raw), `{"id":"`+record.ID+`","direction":"download","filename":"a.txt","size":"abc"`) {
		t.Errorf("record: %s", raw)
	}
	progress, _ := json.Marshal(s.GetProgress(record.ID))
	want := `{"transferId":"` + record.ID + `","status":"pending","bytesTransferred":0,"totalBytes":"abc","chunksReceived":0,"totalChunks":1,"percent":null,"filename":"a.txt","direction":"download"}`
	if string(progress) != want {
		t.Errorf("progress:\n  %s\nwant:\n  %s", progress, want)
	}
}

func TestConcurrentUse(t *testing.T) {
	s, _ := newService(t)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				record, err := s.CreateTransfer(Params{Filename: "f", Size: float64(4), DeviceID: "d"})
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = s.ReceiveChunk(record.ID, float64(0), "QUJD")
				s.GetProgress(record.ID)
				s.ListTransfers(Filter{Status: StatusActive})
				s.HandleMessage("d", goapi.NewObject("action", "chunk", "transfer_id", record.ID, "chunk_index", float64(1), "data", "RA=="))
				s.GetCompletedFilePath(record.ID)
				s.Sweep()
				s.CancelTransfer(record.ID)
			}
		}()
	}
	wg.Wait()
	if n := len(s.ListTransfers(Filter{Status: StatusCancelled})); n != 240 {
		t.Errorf("%d cancelled transfers, want 240", n)
	}
	entries, _ := os.ReadDir(s.opts.Dir)
	if len(entries) != 0 {
		t.Errorf("%d files left in the transfers folder", len(entries))
	}
}

// FuzzHandleMessage sends arbitrary JSON as a device's message. No
// message may stop the console or put a file outside the transfers folder.
func FuzzHandleMessage(f *testing.F) {
	for _, seed := range []string{
		`{"action":"init","filename":"../../x","size":5}`, `{"action":"chunk","transfer_id":"x","data":"QUJD"}`,
		`{"action":"chunk","data":{"length":3,"0":1}}`, `{"action":"chunk","data":[1,"2",null]}`, `{"action":"cancel","transfer_id":5}`,
		`{"action":"browse","path":{"a":1}}`, `{"action":{"toString":1}}`, `[]`, `null`, `"text"`, `{"action":"init","size":{"toString":1}}`,
		`{"action":"init","filename":{"toString":1},"size":"0x10"}`, `{"action":"chunk","chunk_index":"0","data":"ŁQ=="}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		message, err := goapi.Parse(raw)
		if err != nil {
			return
		}
		root := t.TempDir()
		dir := filepath.Join(root, "transfers")
		s := New(Options{Dir: dir, Logger: quiet{}})
		first := s.HandleMessage("dev", message)
		if first == nil || first.Get("action") == nil {
			t.Fatalf("no answer to %s", raw)
		}
		// The same message again as a chunk for each transfer it made
		if body, isObject := message.(*goapi.Object); isObject && body != nil {
			for _, record := range s.ListTransfers(Filter{}) {
				body.Set("transfer_id", record.ID)
				body.Set("action", "chunk")
				s.HandleMessage("dev", body)
			}
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 1 {
			t.Fatalf("the message %s left %d entries beside the transfers folder (%v)", raw, len(entries), err)
		}
		for _, record := range s.ListTransfers(Filter{}) {
			if filepath.Dir(record.TempPath) != dir {
				t.Fatalf("the message %s made the file %q", raw, record.TempPath)
			}
		}
	})
}

// FuzzDecodeBase64 checks the forgiving decoder against the strict one of
// the standard library wherever the text is plain base64, and the file
// name a text becomes.
func FuzzDecodeBase64(f *testing.F) {
	for _, seed := range []string{"", "QUJD", "QUJDRA==", "QU-_", "Q", "QQ=", "QUJD RA", "ŁĽQUJD"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got := decodeBase64(text)
		if len(got) > len(text) {
			t.Fatalf("%d bytes from %d characters", len(got), len(text))
		}
		if strict, err := base64.StdEncoding.DecodeString(text); err == nil && !bytes.Equal(strict, got) {
			t.Fatalf("decodeBase64(%q) = %x, the standard decoder gives %x", text, got, strict)
		}
		if name := safeFileName(text); name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "" {
			t.Fatalf("safeFileName(%q) = %q", text, name)
		}
	})
}

// TestAChunkGivenAsALengthIsCapped covers the one place the service is
// stricter than the old one: an object with a length stands for that many
// bytes, and above 64 MiB it is refused before any memory is taken.
func TestAChunkGivenAsALengthIsCapped(t *testing.T) {
	s, _ := newService(t)
	record, err := s.CreateTransfer(Params{Filename: "big", Size: float64(MaxFileSize), DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	small, err := s.ReceiveChunk(record.ID, float64(0), goapi.NewObject("length", float64(1000), "0", float64(65)))
	if err != nil || small.BytesTransferred != 1000 {
		t.Fatalf("a length of 1000: %+v, %v", small, err)
	}
	for _, length := range []float64{64<<20 + 1, 1e300} {
		_, err := s.ReceiveChunk(record.ID, float64(1), goapi.NewObject("length", length))
		if err == nil || err.Error() != "Array buffer allocation failed" {
			t.Errorf("a length of %v: %v", length, err)
		}
	}
	if got := s.GetProgress(record.ID).BytesTransferred; got != 1000 {
		t.Errorf("%d bytes after the refused chunks", got)
	}
}

// A new service removes the files transfers of an earlier process left in
// the transfers folder, and nothing else (BUGS FT1, DECISIONS A47).
func TestNewRemovesTheLeftovers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "transfers")
	if err := os.MkdirAll(filepath.Join(dir, "0123456789abcdef0123456789abcdef_folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"0123456789abcdef0123456789abcdef_report.pdf": "left",
		"fedcba9876543210fedcba9876543210_file":       "left",
		"notes.txt":                                   "kept",
		"0123456789ABCDEF0123456789ABCDEF_upper.bin":  "kept",
		"0123456789abcdef_short.bin":                  "kept",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	New(Options{Dir: dir, Logger: quiet{}})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := "0123456789ABCDEF0123456789ABCDEF_upper.bin 0123456789abcdef0123456789abcdef_folder 0123456789abcdef_short.bin notes.txt"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("the folder holds %s\nwant %s", got, want)
	}
}
