package filetransfer

import (
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The vectors are lives of transfers as services/fileTransferService.js
// of the Node console lived them (testdata/nodeoracle/filetransfer.js):
// per step the call, what it returned or threw, every record afterwards
// and the files of the transfers folder.

type vectorStep struct {
	Op      string          `json:"op"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
	Filter  json.RawMessage `json:"filter"`
	Index   json.RawMessage `json:"index"`
	Data    json.RawMessage `json:"data"`
	Device  string          `json:"device"`
	Message json.RawMessage `json:"message"`
	MS      int64           `json:"ms"`
	Name    string          `json:"name"`

	Out *string     `json:"out"`
	Err *string     `json:"err"`
	All string      `json:"all"`
	Dir *[][]string `json:"dir"`
}

type vectorFile struct {
	Scenarios []struct {
		Name  string       `json:"name"`
		Steps []vectorStep `json:"steps"`
	} `json:"scenarios"`
	Parsed []struct {
		Value json.RawMessage `json:"value"`
		Out   *string         `json:"out"`
		Err   *string         `json:"err"`
	} `json:"parsed"`
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
	return file
}

// parsed is a JSON value of a vector, nil for one that is missing.
func parsed(t testing.TB, raw json.RawMessage) any {
	t.Helper()
	if raw == nil {
		return nil
	}
	value, err := goapi.Parse(raw)
	if err != nil {
		t.Fatalf("value %s of a vector: %v", raw, err)
	}
	return value
}

// member is object[key], Undefined when the object does not have it.
func member(o *goapi.Object, key string) any {
	if !o.Has(key) {
		return Undefined
	}
	return o.Get(key)
}

// oracleWorld is the Service with the counters the Node script ran on.
type oracleWorld struct {
	t      testing.TB
	dir    string
	clock  int64
	random uint32
	svc    *Service
}

func (w *oracleWorld) Read(p []byte) (int, error) {
	for i := range p {
		w.random = w.random*1103515245 + 12345
		p[i] = byte(w.random >> 16)
	}
	return len(p), nil
}

func (w *oracleWorld) now() time.Time {
	now := time.UnixMilli(w.clock)
	w.clock++
	return now
}

type quiet struct{}

func (quiet) Info(...any) {}
func (quiet) Warn(...any) {}

func (w *oracleWorld) boot() {
	w.svc = New(Options{Dir: w.dir, Now: w.now, Random: w, Logger: quiet{}})
}

func (w *oracleWorld) mask(text string) string {
	return strings.ReplaceAll(text, w.dir, "<DIR>")
}

func (w *oracleWorld) text(v any) *string {
	raw, err := httpx.Marshal(v)
	if err != nil {
		w.t.Fatal(err)
	}
	s := w.mask(string(raw))
	return &s
}

func (w *oracleWorld) run(step vectorStep) (out *string, err error) {
	t := w.t
	id := parsed(t, step.ID)
	textID, _ := id.(string)
	switch step.Op {
	case "boot":
		w.boot()
	case "create":
		params, _ := parsed(t, step.Params).(*goapi.Object)
		record, err := w.svc.CreateTransfer(Params{
			Direction: member(params, "direction"), DeviceID: member(params, "deviceId"), Filename: member(params, "filename"),
			Size: member(params, "size"), MimeType: member(params, "mimeType"), CreatedBy: member(params, "createdBy"),
		})
		if err != nil {
			return nil, err
		}
		return w.text(record), nil
	case "get":
		if record := w.svc.GetTransfer(textID); record != nil {
			return w.text(record), nil
		}
		return w.text(nil), nil
	case "list":
		filter, _ := parsed(t, step.Filter).(*goapi.Object)
		return w.text(w.svc.ListTransfers(Filter{DeviceID: member(filter, "deviceId"), Status: member(filter, "status"), Direction: member(filter, "direction")})), nil
	case "chunk":
		data, index := Undefined, Undefined
		if step.Data != nil {
			data = parsed(t, step.Data)
		}
		if step.Index != nil {
			index = parsed(t, step.Index)
		}
		progress, err := w.svc.ReceiveChunk(id, index, data)
		if err != nil {
			return nil, err
		}
		return w.text(progress), nil
	case "cancel":
		return w.text(w.svc.CancelTransfer(id)), nil
	case "progress":
		if progress := w.svc.GetProgress(textID); progress != nil {
			return w.text(progress), nil
		}
		return w.text(nil), nil
	case "path":
		if path := w.svc.GetCompletedFilePath(textID); path != "" {
			return w.text(path), nil
		}
		return w.text(nil), nil
	case "message":
		return w.text(w.svc.HandleMessage(step.Device, parsed(t, step.Message))), nil
	case "sweep":
		w.svc.Sweep()
	case "advance":
		w.clock += step.MS
	case "rmfile":
		if err := os.Remove(filepath.Join(w.dir, step.Name)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	case "mkdir-in-place":
		if err := os.Mkdir(filepath.Join(w.dir, step.Name), 0o755); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown step %q", step.Op)
	}
	return nil, nil
}

func (w *oracleWorld) dirState() *[][]string {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil
	}
	state := [][]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			state = append(state, []string{entry.Name(), "dir"})
			continue
		}
		content, err := os.ReadFile(filepath.Join(w.dir, entry.Name()))
		if err != nil {
			w.t.Fatal(err)
		}
		sum := sha1.Sum(content)
		state = append(state, []string{entry.Name(), hex.EncodeToString(sum[:])})
	}
	sort.Slice(state, func(i, j int) bool { return state[i][0] < state[j][0] })
	return &state
}

func describe(s *string) string {
	if s == nil {
		return "(nothing)"
	}
	return *s
}

func describeDir(dir *[][]string) string {
	if dir == nil {
		return "(no folder)"
	}
	raw, _ := json.Marshal(*dir)
	return string(raw)
}

// TestNodeVectors replays the lives of transfers and compares every step
// with what the Node service did: the result or error, every record, and
// the files of the transfers folder. The changes the bug fixes made on
// purpose are taken into account first (fixes_test.go): the ones that
// change every step alike as rewrites of Node's answers, the others as the
// console's answers at the steps they name (testdata/vectors_fixed.json).
// FILETRANSFER_RECORD_FIXED=1 writes that file from this run and fails
// for a named step that answers as Node did.
func TestNodeVectors(t *testing.T) {
	file := loadVectors(t)
	recording := os.Getenv("FILETRANSFER_RECORD_FIXED") == "1"
	listed := listedSteps(t)
	fixed := map[int]fixedStep{}
	if !recording {
		fixed = loadFixedSteps(t, listed)
	}
	var written []fixedStep
	rewrites := newRewrites()

	w := &oracleWorld{t: t, dir: filepath.Join(t.TempDir(), "data", "transfers"), clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), random: 20261110}
	w.boot()
	steps, failures := 0, 0
	var unlisted []int
	for _, scenario := range file.Scenarios {
		for i, step := range scenario.Steps {
			number := steps
			steps++
			out, err := w.run(step)
			got := answerOf(w, out, err)
			want := rewrites.apply(step)
			if bug, isListed := listed[number]; isListed {
				if recording {
					entry, differs := fixedFrom(t, got, want)
					if !differs {
						t.Errorf("step %d (%s, step %d) is listed for %s but answers as Node did", number, scenario.Name, i, bug)
					}
					entry.Step, entry.Bug, entry.Scenario, entry.Op = number, bug, scenario.Name, step.Op
					written = append(written, entry)
					continue
				}
				want = fixed[number].onto(t, want)
			}
			problems := got.differences(want)
			if len(problems) > 0 {
				failures++
				if _, isListed := listed[number]; !isListed {
					unlisted = append(unlisted, number)
				}
				if failures <= 15 {
					spec, _ := json.Marshal(step)
					if len(spec) > 600 {
						spec = append(spec[:600], "..."...)
					}
					t.Errorf("step %d (%s, step %d) %s\n  %s", number, scenario.Name, i, spec, strings.Join(problems, "\n  "))
				}
			}
		}
	}
	if failures > 15 {
		t.Errorf("%d of %d steps differ; the first 15 are shown", failures, steps)
	}
	if len(unlisted) > 0 {
		t.Errorf("steps that differ and no fix names: %s", spans(unlisted))
	}
	if steps < 5000 {
		t.Errorf("replayed %d steps, the vector file should hold more than 5000", steps)
	}
	rewrites.check(t)
	if recording {
		writeFixedSteps(t, written)
	}
}

func TestParseSizeVectors(t *testing.T) {
	file := loadVectors(t)
	if len(file.Parsed) < 90 {
		t.Fatalf("%d sizes in the vector file, want more than 90", len(file.Parsed))
	}
	for _, v := range file.Parsed {
		size, err := ParseSize(parsed(t, v.Value))
		switch {
		case v.Err != nil:
			if err == nil || err.Error() != *v.Err {
				t.Errorf("ParseSize(%s): %v, %v; Node threw %q", v.Value, size, err, *v.Err)
			}
		case err != nil:
			t.Errorf("ParseSize(%s): error %v; Node gave %s", v.Value, err, *v.Out)
		default:
			raw, _ := httpx.Marshal(size)
			if string(raw) != *v.Out {
				t.Errorf("ParseSize(%s) = %s; Node gave %s", v.Value, raw, *v.Out)
			}
		}
	}
}
