// Package filetransfer keeps the file transfers between the console and
// devices (services/fileTransferService.js of the old console).
//
// A transfer is a record in memory: who asked for which file of which
// device, how far it is, and where its bytes go. Someone at the console
// starts a transfer, or a device announces one ("init"); the device then
// sends the file in chunks of base64 text, which are appended to one file
// per transfer in the transfers folder, <id>_<file name>. When as many
// bytes have arrived as the transfer announced, it is completed and the
// file can be downloaded.
//
// Records are not stored: a restart of the console forgets every
// transfer, and the files of completed ones stay in the folder.
//
// # Housekeeping
//
// Sweep, which Run calls once a minute, fails a transfer that is active
// and got no chunk for 30 minutes, and removes its file. It drops the
// record of a transfer that was completed, failed or cancelled more than
// an hour ago; the file of a completed one is left where it is. A
// transfer that never got its first chunk stays pending for good.
package filetransfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/safepath"
	"github.com/unitronix/betterdesk-server/logging"
)

const (
	// MaxChunkSize is the size a transfer's chunk count is worked out
	// with: 256 KB.
	MaxChunkSize = 256 * 1024
	// MaxFileSize is the largest file a transfer may announce: 4 GB.
	MaxFileSize = 4 * 1024 * 1024 * 1024
	// TransferTimeout is how long an active transfer may go without a
	// chunk before it fails.
	TransferTimeout = 30 * time.Minute
	// Retention is how long the record of a finished transfer is kept.
	Retention = time.Hour
	// SweepInterval is the time between two runs of the housekeeping.
	SweepInterval = time.Minute

	defaultMimeType = "application/octet-stream"
)

// The states of a transfer.
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Logger takes the package's log lines.
type Logger interface {
	Info(args ...any)
	Warn(args ...any)
}

// Options are the settings of a Service.
type Options struct {
	// Dir is the transfers folder: "data/transfers" in the folder of the
	// old console (not the data folder of DATA_DIR). It is created when
	// missing.
	Dir string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Random gives the random bytes of the transfer ids. Nil means
	// crypto/rand.
	Random io.Reader
	// Logger takes the log lines. Nil logs through the logging package.
	Logger Logger
}

// Service is the registry of transfers of one console process. It is
// safe for concurrent use.
type Service struct {
	opts Options

	mu        sync.Mutex
	transfers map[string]*Transfer
	// order holds the ids in the order the transfers were made
	order []string
}

// New returns a Service and creates the transfers folder.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.Logger == nil {
		opts.Logger = packageLogger{}
	}
	if err := os.MkdirAll(opts.Dir, 0o777); err != nil {
		opts.Logger.Warn("[FileTransfer] cannot create the transfers folder: " + err.Error())
	}
	return &Service{opts: opts, transfers: map[string]*Transfer{}}
}

type packageLogger struct{}

func (packageLogger) Info(args ...any) { logging.Logf("console", "%s", fmt.Sprint(args...)) }
func (packageLogger) Warn(args ...any) { logging.Logf("console", "WARN: %s", fmt.Sprint(args...)) }

// Transfer is the record of one transfer. The values a caller gave are
// kept as they came, so Direction, DeviceID, Size, MimeType and CreatedBy
// hold parsed JSON values (goapi.Parse): usually texts and a number.
type Transfer struct {
	ID string
	// Direction is "upload" (console to device) or "download" (device to
	// console).
	Direction any
	// DeviceID is the device. Undefined leaves the key out of the JSON.
	DeviceID any
	// Filename is the file's name without folders, with every character
	// outside letters, digits, dot, underscore and dash as an underscore.
	Filename string
	// Size is the number of bytes the transfer announced.
	Size     any
	MimeType any
	Status   string
	// BytesTransferred and ChunksReceived count what has arrived.
	BytesTransferred int64
	TotalChunks      float64
	ChunksReceived   int64
	// TempPath is the file the chunks are written to.
	TempPath  string
	CreatedBy any
	// CreatedAt and UpdatedAt are milliseconds since 1970.
	CreatedAt int64
	UpdatedAt int64
}

// MarshalJSON writes the record with the keys the routes have always
// sent: id, direction, deviceId, filename, size, mimeType, status,
// bytesTransferred, totalChunks, chunksReceived, tempPath, createdBy,
// createdAt, updatedAt.
func (t *Transfer) MarshalJSON() ([]byte, error) {
	o := goapi.NewObject("id", t.ID, "direction", t.Direction)
	if t.DeviceID != Undefined {
		o.Set("deviceId", t.DeviceID)
	}
	o.Set("filename", t.Filename)
	o.Set("size", t.Size)
	o.Set("mimeType", t.MimeType)
	o.Set("status", t.Status)
	o.Set("bytesTransferred", float64(t.BytesTransferred))
	o.Set("totalChunks", t.TotalChunks)
	o.Set("chunksReceived", float64(t.ChunksReceived))
	o.Set("tempPath", t.TempPath)
	o.Set("createdBy", t.CreatedBy)
	o.Set("createdAt", float64(t.CreatedAt))
	o.Set("updatedAt", float64(t.UpdatedAt))
	return marshal(o)
}

// Progress is how far a transfer is.
type Progress struct {
	TransferID       string
	Status           string
	BytesTransferred int64
	// TotalBytes is the transfer's size as it was announced.
	TotalBytes     any
	ChunksReceived int64
	TotalChunks    float64
	// Percent is BytesTransferred of TotalBytes; a transfer of no bytes
	// counts as one byte long. It is NaN for a size that is no number.
	Percent   float64
	Filename  string
	Direction any
}

// MarshalJSON writes {transferId, status, bytesTransferred, totalBytes,
// chunksReceived, totalChunks, percent, filename, direction}.
func (p *Progress) MarshalJSON() ([]byte, error) {
	return marshal(goapi.NewObject(
		"transferId", p.TransferID,
		"status", p.Status,
		"bytesTransferred", float64(p.BytesTransferred),
		"totalBytes", p.TotalBytes,
		"chunksReceived", float64(p.ChunksReceived),
		"totalChunks", p.TotalChunks,
		"percent", p.Percent,
		"filename", p.Filename,
		"direction", p.Direction,
	))
}

// Undefined is the value of a member that a request or a message does
// not have, where that is not the same as null: a transfer made without a
// device has no "deviceId" in its JSON, one made with null has null.
var Undefined any = undefinedValue{}

type undefinedValue struct{}

// Params are the values of a new transfer, as a request gave them: parsed
// JSON values, and nil or Undefined for one that is missing.
type Params struct {
	// Direction defaults to "download".
	Direction any
	DeviceID  any
	// Filename is turned into text; missing or empty it is "file".
	Filename any
	// Size is the number of bytes, 0 when missing.
	Size any
	// MimeType defaults to application/octet-stream.
	MimeType any
	// CreatedBy defaults to "system".
	CreatedBy any
}

// ErrNotText is the failure of a value that cannot be turned into text:
// an object with a member named toString.
var ErrNotText = errors.New("Cannot convert object to primitive value")

// CreateTransfer starts a transfer and returns its record.
//
// Errors: "File too large: <size> bytes (max 4294967296)" for a size
// above MaxFileSize, ErrNotText for a size, a file name, a direction or a
// device that has no text, and the errors of package safepath. With
// ErrNotText for the direction or the device the transfer exists all the
// same, pending, and nothing returned its id.
func (s *Service) CreateTransfer(p Params) (*Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.create(p)
	if err != nil {
		return nil, err
	}
	return record.clone(), nil
}

func (s *Service) create(p Params) (*Transfer, error) {
	size, ok := toNumber(p.Size)
	if !ok {
		return nil, ErrNotText
	}
	if size > MaxFileSize {
		text, _ := jsText(p.Size)
		return nil, fmt.Errorf("File too large: %s bytes (max %d)", text, int64(MaxFileSize))
	}
	id := hex.EncodeToString(s.randomBytes(16))

	var name any = "file"
	if truthy(p.Filename) {
		name = p.Filename
	}
	text, ok := jsText(name)
	if !ok {
		return nil, ErrNotText
	}
	safeName := safeFileName(text)
	tempPath, err := safepath.ResolveChild(s.opts.Dir, id+"_"+safeName)
	if err != nil {
		return nil, err
	}
	chunks := math.Ceil(size / MaxChunkSize)
	if chunks == 0 || math.IsNaN(chunks) {
		chunks = 1
	}
	now := s.opts.Now().UnixMilli()
	record := &Transfer{
		ID:          id,
		Direction:   or(p.Direction, "download"),
		DeviceID:    p.DeviceID,
		Filename:    safeName,
		Size:        or(p.Size, float64(0)),
		MimeType:    or(p.MimeType, defaultMimeType),
		Status:      StatusPending,
		TotalChunks: chunks,
		TempPath:    tempPath,
		CreatedBy:   or(p.CreatedBy, "system"),
		CreatedAt:   now,
		UpdatedAt:   s.opts.Now().UnixMilli(),
	}
	// An id that is taken replaces the transfer and keeps its place
	if _, taken := s.transfers[id]; !taken {
		s.order = append(s.order, id)
	}
	s.transfers[id] = record
	// The log line names the direction, the size and the device as text.
	// A value without a text fails the call, with the transfer already
	// in the registry.
	var parts [3]string
	for i, value := range []any{p.Direction, p.Size, p.DeviceID} {
		if parts[i], ok = jsText(value); !ok {
			return nil, ErrNotText
		}
	}
	s.opts.Logger.Info(fmt.Sprintf("[FileTransfer] Created transfer %s: %s %s (%s bytes) for %s", id, parts[0], safeName, parts[1], parts[2]))
	return record, nil
}

// GetTransfer is the record of a transfer, nil when there is none.
func (s *Service) GetTransfer(id string) *Transfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transfers[id].clone()
}

// Filter picks transfers for ListTransfers. Each field is a value of a
// request's query: a text picks the transfers with exactly that value, an
// empty or missing one (nil, Undefined) picks all, and a list or an object
// picks none.
type Filter struct {
	DeviceID  any
	Status    any
	Direction any
}

// ListTransfers lists the transfers the filter picks, newest first.
// Transfers of the same millisecond keep the order they were made in.
func (s *Service) ListTransfers(f Filter) []*Transfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*Transfer{}
	for _, id := range s.order {
		t := s.transfers[id]
		if matches(f.DeviceID, t.DeviceID) && matches(f.Status, t.Status) && matches(f.Direction, t.Direction) {
			out = append(out, t.clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// matches reports whether a transfer's value passes a filter value.
func matches(filter, value any) bool {
	if !truthy(filter) {
		return true
	}
	// filter === value: a text, a number or a flag equals its like; a
	// list or an object equals nothing that was sent on its own
	switch want := filter.(type) {
	case string:
		have, same := value.(string)
		return same && have == want
	case float64:
		have, same := value.(float64)
		return same && have == want
	case bool:
		have, same := value.(bool)
		return same && have == want
	}
	return false
}

// ReceiveChunk adds a chunk to a transfer's file and returns how far the
// transfer is. data is the chunk as base64 text. A chunkIndex of 0 starts
// the file over; every other chunk is appended, whatever its index. The
// transfer is completed once it has as many bytes as it announced.
//
// Errors: "Transfer not found", "Transfer is cancelled", "Transfer is
// failed", the text JavaScript gave for a chunk that is no text, and
// errors of the file.
func (s *Service) ReceiveChunk(transferID any, chunkIndex any, data any) (*ChunkProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, isText := transferID.(string)
	t := s.transfers[id]
	if !isText || t == nil {
		return nil, errors.New("Transfer not found")
	}
	if t.Status == StatusCancelled || t.Status == StatusFailed {
		return nil, errors.New("Transfer is " + t.Status)
	}
	if t.Status == StatusPending {
		t.Status = StatusActive
	}

	chunk, err := bufferFrom(data)
	if err != nil {
		return nil, err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if index, isNumber := chunkIndex.(float64); isNumber && index == 0 {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(t.TempPath, flags, 0o666)
	if err != nil {
		return nil, nodeFileError(err, "open", t.TempPath)
	}
	_, err = file.Write(chunk)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, nodeFileError(err, "write", t.TempPath)
	}

	t.BytesTransferred += int64(len(chunk))
	t.ChunksReceived++
	t.UpdatedAt = s.opts.Now().UnixMilli()
	if float64(t.BytesTransferred) >= number(t.Size) {
		t.Status = StatusCompleted
		s.opts.Logger.Info(fmt.Sprintf("[FileTransfer] Transfer %s completed: %d bytes", id, t.BytesTransferred))
	}
	return &ChunkProgress{
		TransferID:       id,
		ChunksReceived:   t.ChunksReceived,
		TotalChunks:      t.TotalChunks,
		BytesTransferred: t.BytesTransferred,
		TotalBytes:       t.Size,
		Percent:          percent(t),
		Status:           t.Status,
	}, nil
}

// ChunkProgress is the answer to a chunk.
type ChunkProgress struct {
	TransferID       string
	ChunksReceived   int64
	TotalChunks      float64
	BytesTransferred int64
	TotalBytes       any
	Percent          float64
	Status           string
}

// fields are the members of the answer in the order they are sent.
func (p *ChunkProgress) fields() []any {
	return []any{
		"transferId", p.TransferID,
		"chunksReceived", float64(p.ChunksReceived),
		"totalChunks", p.TotalChunks,
		"bytesTransferred", float64(p.BytesTransferred),
		"totalBytes", p.TotalBytes,
		"percent", p.Percent,
		"status", p.Status,
	}
}

// MarshalJSON writes {transferId, chunksReceived, totalChunks,
// bytesTransferred, totalBytes, percent, status}.
func (p *ChunkProgress) MarshalJSON() ([]byte, error) {
	return marshal(goapi.NewObject(p.fields()...))
}

// percent is (bytesTransferred / Math.max(size, 1)) * 100.
func percent(t *Transfer) float64 {
	size := number(t.Size)
	if !math.IsNaN(size) && size < 1 {
		size = 1
	}
	return float64(t.BytesTransferred) / size * 100
}

// CancelTransfer cancels a transfer, whatever state it is in, removes its
// file and reports whether there was such a transfer.
func (s *Service) CancelTransfer(id any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, isText := id.(string)
	t := s.transfers[text]
	if !isText || t == nil {
		return false
	}
	t.Status = StatusCancelled
	t.UpdatedAt = s.opts.Now().UnixMilli()
	removeFile(t.TempPath)
	s.opts.Logger.Info(fmt.Sprintf("[FileTransfer] Transfer %s cancelled", text))
	return true
}

// GetProgress is how far a transfer is, nil when there is none.
func (s *Service) GetProgress(id string) *Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.transfers[id]
	if t == nil {
		return nil
	}
	return &Progress{
		TransferID:       t.ID,
		Status:           t.Status,
		BytesTransferred: t.BytesTransferred,
		TotalBytes:       t.Size,
		ChunksReceived:   t.ChunksReceived,
		TotalChunks:      t.TotalChunks,
		Percent:          percent(t),
		Filename:         t.Filename,
		Direction:        t.Direction,
	}
}

// GetCompletedFilePath is the file of a completed transfer, "" when the
// transfer is not completed or its file is gone.
func (s *Service) GetCompletedFilePath(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.transfers[id]
	if t == nil || t.Status != StatusCompleted {
		return ""
	}
	if _, err := os.Stat(t.TempPath); err != nil {
		return ""
	}
	return t.TempPath
}

// HandleMessage answers a file transfer message of a device: the JSON
// body {action, transfer_id, chunk_index, data, filename, size,
// mime_type, path} after JSON.parse (goapi.Parse).
//
//	init    starts a download transfer of the device: {action: "init_ack", transfer_id, status: "ok"}
//	chunk   adds a chunk: {action: "chunk_ack"} with the members of ChunkProgress
//	cancel  cancels a transfer: {action: "cancel_ack", transfer_id}
//	browse  is passed back for the device: {action: "browse_forward", deviceId, path}
//
// Anything else, and every failure, is {action: "error", error}.
func (s *Service) HandleMessage(deviceID string, message any) *goapi.Object {
	fail := func(text string) *goapi.Object { return goapi.NewObject("action", "error", "error", text) }
	if message == nil {
		return fail("Cannot destructure property 'action' of 'message' as it is null.")
	}
	body, _ := message.(*goapi.Object)
	action := body.Get("action")
	name, _ := action.(string)
	switch name {
	case "init":
		s.mu.Lock()
		record, err := s.create(Params{
			Direction: "download",
			DeviceID:  deviceID,
			Filename:  or(body.Get("filename"), "unknown"),
			Size:      or(body.Get("size"), float64(0)),
			MimeType:  or(body.Get("mime_type"), defaultMimeType),
			CreatedBy: "agent:" + deviceID,
		})
		s.mu.Unlock()
		if err != nil {
			return fail(err.Error())
		}
		return goapi.NewObject("action", "init_ack", "transfer_id", record.ID, "status", "ok")
	case "chunk":
		progress, err := s.ReceiveChunk(body.Get("transfer_id"), or(body.Get("chunk_index"), float64(0)), dataOf(body))
		if err != nil {
			return fail(err.Error())
		}
		return goapi.NewObject(append([]any{"action", "chunk_ack"}, progress.fields()...)...)
	case "cancel":
		s.CancelTransfer(body.Get("transfer_id"))
		answer := goapi.NewObject("action", "cancel_ack")
		if body.Has("transfer_id") {
			answer.Set("transfer_id", body.Get("transfer_id"))
		}
		return answer
	case "browse":
		answer := goapi.NewObject("action", "browse_forward", "deviceId", deviceID)
		if body.Has("path") {
			answer.Set("path", body.Get("path"))
		}
		return answer
	}
	text := "undefined"
	if body.Has("action") {
		var ok bool
		if text, ok = jsText(action); !ok {
			return fail(ErrNotText.Error())
		}
	}
	return fail("Unknown action: " + text)
}

// dataOf is message.data, Undefined for a message that has none.
func dataOf(body *goapi.Object) any {
	if !body.Has("data") {
		return Undefined
	}
	return body.Get("data")
}

// Sweep is one run of the housekeeping (see the package comment).
func (s *Service) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now().UnixMilli()
	kept := s.order[:0]
	for _, id := range s.order {
		t := s.transfers[id]
		if t.Status == StatusActive && now-t.UpdatedAt > TransferTimeout.Milliseconds() {
			t.Status = StatusFailed
			s.opts.Logger.Warn(fmt.Sprintf("[FileTransfer] Transfer %s timed out", id))
			removeFile(t.TempPath)
		}
		finished := t.Status == StatusCompleted || t.Status == StatusFailed || t.Status == StatusCancelled
		if finished && now-t.UpdatedAt > Retention.Milliseconds() {
			delete(s.transfers, id)
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
}

// Run calls Sweep every SweepInterval until ctx ends. The console adds
// it to its background jobs.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep()
		}
	}
}

func (t *Transfer) clone() *Transfer {
	if t == nil {
		return nil
	}
	copied := *t
	return &copied
}

func (s *Service) randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.opts.Random, buf); err != nil {
		panic(fmt.Errorf("filetransfer: random source: %w", err))
	}
	return buf
}

// removeFile deletes a transfer's file when it is there. A file that
// cannot be deleted stays, and so does a folder in its place.
func removeFile(path string) {
	if info, err := os.Lstat(path); err != nil || info.IsDir() {
		return
	}
	_ = os.Remove(path)
}

// safeFileName is the last part of a path with every character outside
// letters, digits, dot, underscore and dash as an underscore, one per
// UTF-16 code unit, and "file" when nothing is left.
func safeFileName(name string) string {
	base := baseName(name)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

// truthy is !!v.
func truthy(v any) bool {
	return v != Undefined && goapi.Truthy(v)
}

// or is a || b.
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

// marshal writes a JSON value the way JSON.stringify does.
func marshal(v any) ([]byte, error) {
	return httpx.Marshal(goapipath.Plain(v))
}
