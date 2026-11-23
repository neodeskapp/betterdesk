package filetransfer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
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
	if got := s.GetTransfer(pending).Status; got != StatusPending {
		t.Fatalf("after exactly 30 minutes the pending transfer is %s", got)
	}
	*now = now.Add(time.Millisecond)
	s.Sweep()
	if got := s.GetTransfer(active).Status; got != StatusFailed {
		t.Fatalf("after 30 minutes and a millisecond the active transfer is %s", got)
	}
	// A transfer that never got a chunk times out as well (BUGS FT4)
	if got := s.GetTransfer(pending).Status; got != StatusFailed {
		t.Fatalf("after 30 minutes and a millisecond the pending transfer is %s", got)
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
	if s.GetTransfer(done) != nil || s.GetTransfer(active) != nil || s.GetTransfer(pending) != nil {
		t.Error("finished transfers are still listed after more than an hour")
	}
	// The file of a completed transfer goes with its record (BUGS FT4)
	if _, err := os.Stat(doneFile); !os.IsNotExist(err) {
		t.Errorf("the file of the completed transfer stays after its record: %v", err)
	}
	if list := s.ListTransfers(Filter{}); len(list) != 0 {
		t.Errorf("the list holds %d transfers", len(list))
	}
	if entries, _ := os.ReadDir(s.opts.Dir); len(entries) != 0 {
		t.Errorf("%d files left in the transfers folder", len(entries))
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
	record, _ := s.CreateTransfer(Params{Filename: "a.txt", DeviceID: Undefined, Size: "12"})
	raw, _ := json.Marshal(record)
	if strings.Contains(string(raw), "deviceId") || !strings.HasPrefix(string(raw), `{"id":"`+record.ID+`","direction":"download","filename":"a.txt","size":"12"`) {
		t.Errorf("record: %s", raw)
	}
	// The record names no path on the server (BUGS FT6)
	if strings.Contains(string(raw), "tempPath") || strings.Contains(string(raw), s.opts.Dir) {
		t.Errorf("record with the path of its file: %s", raw)
	}
	progress, _ := json.Marshal(s.GetProgress(record.ID))
	want := `{"transferId":"` + record.ID + `","status":"pending","bytesTransferred":0,"totalBytes":"12","chunksReceived":0,"totalChunks":1,"percent":0,"filename":"a.txt","direction":"download"}`
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

// FuzzDecodeBase64 checks the chunk decoder against the one of the
// standard library: padded standard base64 without line ends (which the
// standard decoder skips) gives the same bytes, and the decoder takes
// nothing that is no standard base64 without its padding. It checks the
// file name a text becomes too.
func FuzzDecodeBase64(f *testing.F) {
	for _, seed := range []string{"", "QUJD", "QUJDRA==", "QU-_", "Q", "QQ=", "QUJD RA", "ŁĽQUJD"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got, err := decodeBase64(text)
		if strict, strictErr := base64.StdEncoding.DecodeString(text); strictErr == nil && !strings.ContainsAny(text, "\r\n") && (err != nil || !bytes.Equal(strict, got)) {
			t.Fatalf("decodeBase64(%q) = %x, %v; the standard decoder gives %x", text, got, err, strict)
		}
		if raw, rawErr := base64.RawStdEncoding.DecodeString(strings.TrimRight(text, "=")); err == nil && (rawErr != nil || !bytes.Equal(raw, got)) {
			t.Fatalf("decodeBase64(%q) = %x; the standard decoder without padding gives %x, %v", text, got, raw, rawErr)
		}
		if name := safeFileName(text); name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "" {
			t.Fatalf("safeFileName(%q) = %q", text, name)
		}
	})
}

// TestAChunkGivenAsALengthIsCapped: an object with a length stands for
// that many bytes, and above MaxChunkSize it is refused before any memory
// is taken (BUGS FT3).
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
	for _, length := range []float64{MaxChunkSize + 1, 64<<20 + 1, 1e300} {
		_, err := s.ReceiveChunk(record.ID, float64(1), goapi.NewObject("length", length))
		if err == nil || err.Error() != "Chunk too large (max 262144 bytes)" {
			t.Errorf("a length of %v: %v", length, err)
		}
		// The length is refused before the bytes are made
		if chunk, err := bufferFrom(goapi.NewObject("length", length)); chunk != nil || err != errChunkTooLarge {
			t.Errorf("bufferFrom of a length of %v: %d bytes, %v", length, len(chunk), err)
		}
	}
	if got := s.GetProgress(record.ID).BytesTransferred; got != 1000 {
		t.Errorf("%d bytes after the refused chunks", got)
	}
}

// A chunk may not hold more than MaxChunkSize bytes nor more bytes than
// the transfer announced, and a completed transfer takes no chunk that
// starts the file over; a refused chunk writes nothing (BUGS FT3).
func TestChunkBeyondTheSizeIsRefused(t *testing.T) {
	s, _ := newService(t)
	record, err := s.CreateTransfer(Params{Filename: "note.txt", Size: float64(8), DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	chunk := func(index float64, data string) error {
		_, err := s.ReceiveChunk(record.ID, index, base64.StdEncoding.EncodeToString([]byte(data)))
		return err
	}
	if err := chunk(0, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := chunk(1, "world"); err == nil || err.Error() != "Chunk goes beyond the size of the transfer (8 bytes)" {
		t.Errorf("a chunk beyond the size: %v", err)
	}
	if err := chunk(1, "abc"); err != nil {
		t.Errorf("the last chunk: %v", err)
	}
	if got := s.GetTransfer(record.ID); got.Status != StatusCompleted || got.BytesTransferred != 8 || got.ChunksReceived != 2 {
		t.Errorf("after the last chunk: %s, %d bytes in %d chunks", got.Status, got.BytesTransferred, got.ChunksReceived)
	}
	if err := chunk(0, "again"); err == nil || err.Error() != "Transfer is completed" {
		t.Errorf("a first chunk again after the end: %v", err)
	}
	if err := chunk(2, "!"); err == nil || err.Error() != "Chunk goes beyond the size of the transfer (8 bytes)" {
		t.Errorf("a byte more after the end: %v", err)
	}
	content, err := os.ReadFile(s.GetCompletedFilePath(record.ID))
	if err != nil || string(content) != "helloabc" {
		t.Errorf("the file holds %q, %v", content, err)
	}

	big, err := s.CreateTransfer(Params{Filename: "big.bin", Size: float64(MaxFileSize), DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReceiveChunk(big.ID, float64(0), base64.StdEncoding.EncodeToString(make([]byte, MaxChunkSize+1))); err == nil || err.Error() != "Chunk too large (max 262144 bytes)" {
		t.Errorf("a chunk above the chunk size: %v", err)
	}
	if _, err := s.ReceiveChunk(big.ID, float64(0), base64.StdEncoding.EncodeToString(make([]byte, MaxChunkSize))); err != nil {
		t.Errorf("a chunk of the chunk size: %v", err)
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

// A device's message reaches only the transfers of that device: a chunk
// or a cancel for another device's transfer is refused as one for no
// transfer, and a device sends no chunk into an upload, which goes the
// other way (BUGS FT2).
func TestAMessageReachesOnlyTheDevicesOwnTransfers(t *testing.T) {
	s, _ := newService(t)
	init := s.HandleMessage("dev-B", goapi.NewObject("action", "init", "filename", "talk.bin", "size", float64(8)))
	id, _ := init.Get("transfer_id").(string)
	if id == "" {
		t.Fatalf("init: %v", init)
	}
	answer := func(device string, message *goapi.Object) string {
		raw, _ := json.Marshal(s.HandleMessage(device, message))
		return string(raw)
	}
	notFound := `{"action":"error","error":"Transfer not found"}`
	if got := answer("dev-C", goapi.NewObject("action", "chunk", "transfer_id", id, "chunk_index", float64(0), "data", "QUJDRA==")); got != notFound {
		t.Errorf("another device's chunk: %s", got)
	}
	if got := answer("dev-C", goapi.NewObject("action", "cancel", "transfer_id", id)); got != notFound {
		t.Errorf("another device's cancel: %s", got)
	}
	if record := s.GetTransfer(id); record.Status != StatusPending || record.BytesTransferred != 0 {
		t.Errorf("the transfer after the other device: %s, %d bytes", record.Status, record.BytesTransferred)
	}
	if got := answer("dev-B", goapi.NewObject("action", "chunk", "transfer_id", id, "chunk_index", float64(0), "data", "QUJDRA==")); !strings.Contains(got, `"bytesTransferred":4`) {
		t.Errorf("the device's own chunk: %s", got)
	}
	if got := answer("dev-B", goapi.NewObject("action", "cancel", "transfer_id", id)); got != `{"action":"cancel_ack","transfer_id":"`+id+`"}` {
		t.Errorf("the device's own cancel: %s", got)
	}

	// A transfer someone started for a device: the device id as a number
	// is the device's too
	upload, err := s.CreateTransfer(Params{Direction: "upload", DeviceID: "dev-B", Filename: "to-device.txt", Size: float64(4)})
	if err != nil {
		t.Fatal(err)
	}
	if got := answer("dev-B", goapi.NewObject("action", "chunk", "transfer_id", upload.ID, "data", "QUJDRA==")); got != `{"action":"error","error":"Transfer is an upload"}` {
		t.Errorf("a chunk into an upload: %s", got)
	}
	download, err := s.CreateTransfer(Params{Direction: "download", DeviceID: float64(123), Filename: "from-device.txt", Size: float64(4)})
	if err != nil {
		t.Fatal(err)
	}
	if got := answer("123", goapi.NewObject("action", "chunk", "transfer_id", download.ID, "data", "QUJDRA==")); !strings.Contains(got, `"status":"completed"`) {
		t.Errorf("a chunk of the device 123: %s", got)
	}
	unbound, err := s.CreateTransfer(Params{Filename: "nobody.txt", DeviceID: Undefined, Size: float64(4)})
	if err != nil {
		t.Fatal(err)
	}
	if got := answer("undefined", goapi.NewObject("action", "chunk", "transfer_id", unbound.ID, "data", "QUJDRA==")); got != notFound {
		t.Errorf("a chunk into a transfer of no device: %s", got)
	}
}

// A transfer whose direction or device has no text is refused before it
// is stored, and a long file name is cut so the transfer's file can be
// written (BUGS FT5).
func TestATransferWithoutTextIsNotStored(t *testing.T) {
	s, _ := newService(t)
	noText := goapi.NewObject("toString", float64(1))
	for name, p := range map[string]Params{
		"device":    {Filename: "a.txt", DeviceID: noText},
		"direction": {Filename: "a.txt", DeviceID: "d", Direction: noText},
	} {
		if _, err := s.CreateTransfer(p); err != ErrNotText {
			t.Errorf("a %s without a text: %v", name, err)
		}
	}
	if list := s.ListTransfers(Filter{}); len(list) != 0 {
		t.Errorf("%d transfers stored, the first %+v", len(list), list[0])
	}

	record, err := s.CreateTransfer(Params{Filename: strings.Repeat("n", 300) + ".txt", DeviceID: "d", Size: float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Filename) != 200 || !strings.HasPrefix(record.Filename, "nnn") || !strings.HasSuffix(record.Filename, "n.txt") {
		t.Errorf("a file name of %d characters: %q", len(record.Filename), record.Filename)
	}
	if _, err := s.ReceiveChunk(record.ID, float64(0), "QUI="); err != nil {
		t.Errorf("a chunk for a transfer with a long name: %v", err)
	}
}

// A chunk's text is plain base64: the standard alphabet with its padding
// at the end, or without padding. Anything else is refused and writes
// nothing (BUGS FT9).
func TestAChunkThatIsNotBase64IsRefused(t *testing.T) {
	s, _ := newService(t)
	record, err := s.CreateTransfer(Params{Filename: "b.bin", Size: float64(1000), DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"ŁQ==", "😀QUJD", "QU JD", "QUJD\n", "QUJD\nRA", "QUJD\r\nRA", "QU-_", "QQ=A", "Q===", "Q", "QUJDR", "QQ="} {
		if _, err := s.ReceiveChunk(record.ID, float64(1), text); err == nil || err.Error() != "Chunk is no base64 text" {
			t.Errorf("chunk %q: %v", text, err)
		}
	}
	if got := s.GetTransfer(record.ID); got.BytesTransferred != 0 || got.ChunksReceived != 0 {
		t.Errorf("after the refused chunks: %d bytes in %d chunks", got.BytesTransferred, got.ChunksReceived)
	}
	for text, bytes := range map[string]int64{"": 0, "QUJD": 3, "QUI=": 2, "QUI": 2, "QQ==": 1, "QQ": 1, "+/+/": 3} {
		before := s.GetTransfer(record.ID).BytesTransferred
		if _, err := s.ReceiveChunk(record.ID, float64(1), text); err != nil {
			t.Errorf("chunk %q: %v", text, err)
		} else if got := s.GetTransfer(record.ID).BytesTransferred - before; got != bytes {
			t.Errorf("chunk %q gave %d bytes, want %d", text, got, bytes)
		}
	}
}

// A transfer's size is a number from 0 up, or 0 when none is given. Any
// other size is refused before the transfer is stored, from a device's
// init as from a caller, so a transfer always has a limit and an end
// (BUGS RV42).
func TestASizeThatIsNoNumberIsRefused(t *testing.T) {
	s, _ := newService(t)
	for _, size := range []any{"abc", "12abc", goapi.NewObject(), []any{float64(1), float64(2)}, float64(-1), "-5"} {
		if _, err := s.CreateTransfer(Params{Filename: "x.bin", DeviceID: "d", Size: size}); err == nil || !strings.HasPrefix(err.Error(), "Invalid file size") {
			t.Errorf("CreateTransfer with the size %#v: %v", size, err)
		}
		message := goapi.NewObject("action", "init", "filename", "x.bin", "size", size)
		raw, _ := json.Marshal(s.HandleMessage("dev", message))
		if !strings.HasPrefix(string(raw), `{"action":"error","error":"Invalid file size`) {
			t.Errorf("init with the size %#v: %s", size, raw)
		}
	}
	if list := s.ListTransfers(Filter{}); len(list) != 0 {
		t.Fatalf("%d transfers stored, the first %+v", len(list), list[0])
	}
	for _, size := range []any{Undefined, nil, "", float64(0), "12", float64(MaxFileSize)} {
		if _, err := s.CreateTransfer(Params{Filename: "x.bin", DeviceID: "d", Size: size}); err != nil {
			t.Errorf("CreateTransfer with the size %#v: %v", size, err)
		}
	}
}

// A transfer's type is short text, or the default when none is given. A
// type that is no text or longer than 255 characters is refused before
// the transfer is stored, so a record holds no large value (BUGS RV48).
func TestATypeThatIsNoShortTextIsRefused(t *testing.T) {
	s, _ := newService(t)
	refused := "mime_type must be text of at most 255 characters"
	for _, mime := range []any{float64(5), true, goapi.NewObject("x", float64(1)), []any{"text/plain"}, strings.Repeat("a", 256), strings.Repeat("é", 256)} {
		if _, err := s.CreateTransfer(Params{Filename: "x.bin", DeviceID: "d", Size: float64(1), MimeType: mime}); err == nil || err.Error() != refused {
			t.Errorf("CreateTransfer with the type %.40v: %v", mime, err)
		}
		message := goapi.NewObject("action", "init", "filename", "x.bin", "size", float64(1), "mime_type", mime)
		raw, _ := json.Marshal(s.HandleMessage("dev", message))
		if string(raw) != `{"action":"error","error":"`+refused+`"}` {
			t.Errorf("init with the type %.40v: %s", mime, raw)
		}
	}
	if list := s.ListTransfers(Filter{}); len(list) != 0 {
		t.Fatalf("%d transfers stored, the first %+v", len(list), list[0])
	}
	for mime, want := range map[any]string{nil: defaultMimeType, "": defaultMimeType, float64(0): defaultMimeType, false: defaultMimeType, "text/plain": "text/plain", strings.Repeat("é", 255): strings.Repeat("é", 255)} {
		record, err := s.CreateTransfer(Params{Filename: "x.bin", DeviceID: "d", Size: float64(1), MimeType: mime})
		if err != nil || record.MimeType != want {
			t.Errorf("CreateTransfer with the type %.40v: %v, %v", mime, record, err)
		}
	}
}

// A device has at most MaxOpenTransfersPerDevice pending or active
// transfers and the console MaxOpenTransfers; a start beyond them is
// refused, by the people's call and by the device's init message, and not
// stored. A transfer that is completed, cancelled or failed frees its
// place (BUGS RV48, DECISIONS A100).
func TestOpenTransfersAreCapped(t *testing.T) {
	s, now := newService(t)
	start := func(device string) error {
		_, err := s.CreateTransfer(Params{Filename: "x.bin", DeviceID: device, Size: float64(4)})
		return err
	}
	for i := 0; i < MaxOpenTransfersPerDevice; i++ {
		if err := start("dev"); err != nil {
			t.Fatalf("transfer %d of the device: %v", i+1, err)
		}
	}
	perDevice := "Too many open file transfers for this device (at most 10 pending or active)"
	if err := start("dev"); err == nil || err.Error() != perDevice || !errors.Is(err, ErrTooManyTransfers) {
		t.Errorf("transfer 11 of the device: %v", err)
	}
	init := goapi.NewObject("action", "init", "filename", "x.bin", "size", float64(4))
	answer, status := s.HandleMessageStatus("dev", init)
	if raw, _ := json.Marshal(answer); string(raw) != `{"action":"error","error":"`+perDevice+`"}` || status != http.StatusTooManyRequests {
		t.Errorf("init beyond the device's cap: %d %s", status, raw)
	}
	if err := start("other"); err != nil {
		t.Errorf("another device's first transfer: %v", err)
	}

	// Finished transfers free their places: one completed, one cancelled,
	// and the seven that waited too long failed by the sweep, while one
	// that got a chunk stays active
	list := s.ListTransfers(Filter{DeviceID: "dev"})
	if len(list) != MaxOpenTransfersPerDevice {
		t.Fatalf("%d transfers of the device stored", len(list))
	}
	if _, err := s.ReceiveChunk(list[0].ID, float64(0), "AAAAAA=="); err != nil {
		t.Fatal(err)
	}
	s.CancelTransfer(list[1].ID)
	*now = now.Add(TransferTimeout + time.Minute)
	if _, err := s.ReceiveChunk(list[2].ID, float64(0), "AA=="); err != nil {
		t.Fatal(err)
	}
	s.Sweep()
	if got := s.GetTransfer(list[2].ID); got == nil || got.Status != StatusActive {
		t.Fatalf("the transfer that got a chunk: %+v", got)
	}
	for i := 0; i < MaxOpenTransfersPerDevice-1; i++ {
		answer, status := s.HandleMessageStatus("dev", init)
		if answer.Get("action") != "init_ack" || status != http.StatusOK {
			raw, _ := json.Marshal(answer)
			t.Fatalf("init %d after transfers finished: %d %s", i+1, status, raw)
		}
	}
	if err := start("dev"); err == nil || err.Error() != perDevice {
		t.Errorf("a start beyond the cap after the freed places were taken: %v", err)
	}

	// The cap in all: other devices fill the console up to MaxOpenTransfers
	open := 0
	for _, record := range s.ListTransfers(Filter{}) {
		if record.Status == StatusPending || record.Status == StatusActive {
			open++
		}
	}
	for i := 0; open < MaxOpenTransfers; i++ {
		device := "fill" + strconv.Itoa(i/MaxOpenTransfersPerDevice)
		if err := start(device); err != nil {
			t.Fatalf("open transfer %d: %v", open+1, err)
		}
		open++
	}
	inAll := "Too many open file transfers (at most 200 pending or active)"
	if err := start("fresh"); err == nil || err.Error() != inAll || !errors.Is(err, ErrTooManyTransfers) {
		t.Errorf("transfer 201: %v", err)
	}
	answer, status = s.HandleMessageStatus("fresh2", init)
	if raw, _ := json.Marshal(answer); string(raw) != `{"action":"error","error":"`+inAll+`"}` || status != http.StatusTooManyRequests {
		t.Errorf("init beyond the cap in all: %d %s", status, raw)
	}
	if got := len(s.ListTransfers(Filter{DeviceID: "fresh"})) + len(s.ListTransfers(Filter{DeviceID: "fresh2"})); got != 0 {
		t.Errorf("%d refused transfers stored", got)
	}

	// Other failures of an init keep status 200
	_, status = s.HandleMessageStatus("dev", goapi.NewObject("action", "nope"))
	if status != http.StatusOK {
		t.Errorf("an unknown action: status %d", status)
	}
}
