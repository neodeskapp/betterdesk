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
// transfer, and New removes the files they left in the folder.
//
// # Housekeeping
//
// Sweep, which Run calls once a minute, fails a transfer that is pending
// or active and got no chunk for 30 minutes, and removes its file. It
// drops the record of a transfer that was completed, failed or cancelled
// more than an hour ago, with its file.
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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	// maxMimeType is the longest type a transfer keeps, in characters.
	maxMimeType = 255
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

// New returns a Service, creates the transfers folder and removes the
// files of the transfers an earlier process made there: the service knows
// no transfer before it starts, so nobody can reach them any more.
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
	removeLeftovers(opts.Dir, opts.Logger)
	return &Service{opts: opts, transfers: map[string]*Transfer{}}
}

// transferFile is the start of a transfer file's name: the transfer's id,
// 32 hex digits, and an underscore.
var transferFile = regexp.MustCompile(`^[0-9a-f]{32}_`)

// removeLeftovers deletes the files of the folder whose names are those
// of transfer files. Anything else in the folder stays.
func removeLeftovers(dir string, logger Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	removed := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !transferFile.MatchString(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			logger.Warn("[FileTransfer] cannot remove a file an earlier transfer left: " + err.Error())
			continue
		}
		removed++
	}
	if removed > 0 {
		logger.Info(fmt.Sprintf("[FileTransfer] Removed %d files earlier transfers left", removed))
	}
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

// MarshalJSON writes the record with the keys the routes send: id,
// direction, deviceId, filename, size, mimeType, status,
// bytesTransferred, totalChunks, chunksReceived, createdBy, createdAt,
// updatedAt. The path of the file on the server is not among them.
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
	// counts as one byte long.
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
	// MimeType defaults to application/octet-stream. Given, it is text of
	// at most 255 characters.
	MimeType any
	// CreatedBy defaults to "system".
	CreatedBy any
}

// ErrNotText is the failure of a value that cannot be turned into text:
// an object with a member named toString.
var ErrNotText = errors.New("Cannot convert object to primitive value")

// errMimeType refuses a type that is no short text.
var errMimeType = fmt.Errorf("mime_type must be text of at most %d characters", maxMimeType)

// CreateTransfer starts a transfer and returns its record.
//
// Errors: "Invalid file size: <size>" for a size that is no number from 0
// up, "File too large: <size> bytes (max 4294967296)" for a size above
// MaxFileSize, errMimeType for a type that is no text of at most
// maxMimeType characters, ErrNotText for a size, a file name, a direction or a
// device that has no text, and the errors of package safepath. A refused
// transfer is not stored. The safe file name is cut to maxFileName
// bytes, so the transfer's file can be written.
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
	// The size the transfer keeps, 0 when none was given, must be a number
	// from 0 up: any other would give its chunks no limit and it no end
	storedSize := or(p.Size, float64(0))
	if n := number(storedSize); math.IsNaN(n) || n < 0 {
		text, _ := jsText(storedSize)
		return nil, fmt.Errorf("Invalid file size: %s", text)
	}
	mimeType := or(p.MimeType, defaultMimeType)
	if text, isText := mimeType.(string); !isText || utf8.RuneCountInString(text) > maxMimeType {
		return nil, errMimeType
	}

	var name any = "file"
	if truthy(p.Filename) {
		name = p.Filename
	}
	text, ok := jsText(name)
	if !ok {
		return nil, ErrNotText
	}
	// The log line names the direction, the size and the device as text
	var parts [3]string
	for i, value := range []any{p.Direction, p.Size, p.DeviceID} {
		if parts[i], ok = jsText(value); !ok {
			return nil, ErrNotText
		}
	}
	safeName := shortName(safeFileName(text))
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
		Size:        storedSize,
		MimeType:    mimeType,
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
// transfer is completed once it has as many bytes as it announced. A
// chunk takes at most MaxChunkSize bytes and no byte beyond the size the
// transfer announced.
//
// Errors: "Transfer not found", "Transfer is cancelled", "Transfer is
// failed", "Transfer is completed" for a chunk index 0 after the end,
// "Chunk too large (max 262144 bytes)", "Chunk goes beyond the size of
// the transfer (<size> bytes)", the text JavaScript gave for a chunk that
// is no text, and errors of the file. A refused chunk writes nothing.
func (s *Service) ReceiveChunk(transferID any, chunkIndex any, data any) (*ChunkProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, isText := transferID.(string)
	t := s.transfers[id]
	if !isText || t == nil {
		return nil, errors.New("Transfer not found")
	}
	return s.receive(id, t, chunkIndex, data)
}

// receiveFrom is ReceiveChunk for a chunk a device sent: the transfer
// must be the device's and no upload.
func (s *Service) receiveFrom(deviceID string, transferID any, chunkIndex any, data any) (*ChunkProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := transferID.(string)
	t := s.transfers[id]
	if t == nil || !isDevicesTransfer(t, deviceID) {
		return nil, errors.New("Transfer not found")
	}
	if t.Direction == "upload" {
		return nil, errors.New("Transfer is an upload")
	}
	return s.receive(id, t, chunkIndex, data)
}

// isDevicesTransfer reports whether the transfer is the device's: its
// device, as text, is the device's id.
func isDevicesTransfer(t *Transfer, deviceID string) bool {
	switch device := t.DeviceID.(type) {
	case string:
		return device == deviceID
	case float64:
		text, _ := jsText(device)
		return text == deviceID
	}
	return false
}

// receive adds a chunk to the transfer t with the id; s.mu is held.
func (s *Service) receive(id string, t *Transfer, chunkIndex any, data any) (*ChunkProgress, error) {
	if t.Status == StatusCancelled || t.Status == StatusFailed {
		return nil, errors.New("Transfer is " + t.Status)
	}
	index, isNumber := chunkIndex.(float64)
	startsOver := isNumber && index == 0
	if startsOver && t.Status == StatusCompleted {
		return nil, errors.New("Transfer is completed")
	}
	if t.Status == StatusPending {
		t.Status = StatusActive
	}

	chunk, err := bufferFrom(data)
	if err != nil {
		return nil, err
	}
	if len(chunk) > MaxChunkSize {
		return nil, errChunkTooLarge
	}
	if size := number(t.Size); float64(t.BytesTransferred+int64(len(chunk))) > size {
		text, _ := jsText(t.Size)
		return nil, fmt.Errorf("Chunk goes beyond the size of the transfer (%s bytes)", text)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if startsOver {
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
	s.cancel(text, t)
	return true
}

// cancelFrom is CancelTransfer for a cancel a device sent. It cancels the
// transfer when it is the device's and reports false for another device's
// transfer; an id of no transfer changes nothing.
func (s *Service) cancelFrom(deviceID string, id any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, _ := id.(string)
	t := s.transfers[text]
	switch {
	case t == nil:
		return true
	case !isDevicesTransfer(t, deviceID):
		return false
	}
	s.cancel(text, t)
	return true
}

// cancel cancels the transfer t with the id; s.mu is held.
func (s *Service) cancel(id string, t *Transfer) {
	t.Status = StatusCancelled
	t.UpdatedAt = s.opts.Now().UnixMilli()
	removeFile(t.TempPath)
	s.opts.Logger.Info(fmt.Sprintf("[FileTransfer] Transfer %s cancelled", id))
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
//	chunk   adds a chunk to a download of the device: {action: "chunk_ack"} with the members of ChunkProgress
//	cancel  cancels a transfer of the device: {action: "cancel_ack", transfer_id}
//	browse  is passed back for the device: {action: "browse_forward", deviceId, path}
//
// A chunk or a cancel for another device's transfer is refused as one
// for no transfer ("Transfer not found"). Anything else, and every
// failure, is {action: "error", error}.
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
		progress, err := s.receiveFrom(deviceID, body.Get("transfer_id"), or(body.Get("chunk_index"), float64(0)), dataOf(body))
		if err != nil {
			return fail(err.Error())
		}
		return goapi.NewObject(append([]any{"action", "chunk_ack"}, progress.fields()...)...)
	case "cancel":
		if !s.cancelFrom(deviceID, body.Get("transfer_id")) {
			return fail("Transfer not found")
		}
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
		if (t.Status == StatusActive || t.Status == StatusPending) && now-t.UpdatedAt > TransferTimeout.Milliseconds() {
			t.Status = StatusFailed
			s.opts.Logger.Warn(fmt.Sprintf("[FileTransfer] Transfer %s timed out", id))
			removeFile(t.TempPath)
		}
		finished := t.Status == StatusCompleted || t.Status == StatusFailed || t.Status == StatusCancelled
		if finished && now-t.UpdatedAt > Retention.Milliseconds() {
			removeFile(t.TempPath)
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

// maxFileName is the longest safe file name: with the id and the
// underscore in front, a transfer's file name stays below the 255 bytes
// file systems allow.
const maxFileName = 200

// shortName cuts a safe file name to maxFileName bytes. An extension of
// up to 16 bytes stays at the end.
func shortName(name string) string {
	if len(name) <= maxFileName {
		return name
	}
	ext := ""
	if dot := strings.LastIndexByte(name, '.'); dot > 0 && len(name)-dot <= 16 {
		ext = name[dot:]
	}
	return name[:maxFileName-len(ext)] + ext
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
