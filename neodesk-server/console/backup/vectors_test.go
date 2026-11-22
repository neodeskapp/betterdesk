package backup

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// The vectors are what the Node console's own modules answered
// (testdata/README.md). Every one is replayed.

type jsErr struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func loadVectors(t *testing.T, name string, v any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type archiveVectors struct {
	Tar []struct {
		Entries []struct {
			Name string `json:"name"`
			Data string `json:"data"`
			Mode *int64 `json:"mode"`
		} `json:"entries"`
		Mtime int64  `json:"mtime"`
		Tar   string `json:"tar"`
		Error *jsErr `json:"error"`
	} `json:"tar"`
	Extract []struct {
		Input string      `json:"input"`
		Files [][2]string `json:"files"`
		Error *jsErr      `json:"error"`
	} `json:"extract"`
	Gunzip []struct {
		Input  string `json:"input"`
		Output string `json:"output"`
		Error  *jsErr `json:"error"`
	} `json:"gunzip"`
	// Limits is extractTarGz(input, options) with maxBytes and maxEntries.
	Limits []struct {
		Input   string `json:"input"`
		Options struct {
			MaxBytes   float64 `json:"maxBytes"`
			MaxEntries float64 `json:"maxEntries"`
		} `json:"options"`
		Files [][2]string `json:"files"`
		Error *jsErr      `json:"error"`
	} `json:"limits"`
	// Env is the module loaded with BACKUP_MAX_DECOMPRESSED_BYTES and
	// BACKUP_MAX_ENTRIES (null: unset): the limits it took and what it made
	// of each of the archives.
	Env struct {
		Archives []string `json:"archives"`
		Cases    []struct {
			Bytes      *string `json:"bytes"`
			Entries    *string `json:"entries"`
			MaxBytes   string  `json:"maxBytes"`
			MaxEntries string  `json:"maxEntries"`
			Results    []struct {
				Files [][2]string `json:"files"`
				Error *jsErr      `json:"error"`
			} `json:"results"`
		} `json:"cases"`
	} `json:"env"`
}

// extractDiff compares what ExtractTarGz gave with Node's answer: its
// files in order, or its error. "" when they agree.
func extractDiff(files *Files, err error, want [][2]string, wantErr *jsErr) string {
	if wantErr != nil {
		if err == nil || err.Error() != wantErr.Message {
			return fmt.Sprintf("want error %q, got %v", wantErr.Message, err)
		}
		return ""
	}
	if err != nil {
		return err.Error()
	}
	var got [][2]string
	for _, name := range files.Names() {
		data, _ := files.Get(name)
		got = append(got, [2]string{name, base64.StdEncoding.EncodeToString(data)})
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d files, want %d: %q", len(got), len(want), got)
	}
	for k := range got {
		if got[k] != want[k] {
			return fmt.Sprintf("file %d: got %q, want %q", k, got[k][0], want[k][0])
		}
	}
	return ""
}

// TestExtractLimitsMatchNode: the options of extractTarGz, maxBytes and
// maxEntries, with fractions and at the edges of the tar file's length
// and of the number of regular files.
func TestExtractLimitsMatchNode(t *testing.T) {
	var v archiveVectors
	loadVectors(t, "archive.json.gz", &v)
	if len(v.Limits) < 3000 {
		t.Fatalf("only %d limit vectors", len(v.Limits))
	}
	failed, kinds, counted := 0, map[string]int{}, 0
	for i, c := range v.Limits {
		input := unb64(t, c.Input)
		files, err := ExtractTarGz(input, Limits{MaxBytes: c.Options.MaxBytes, MaxEntries: c.Options.MaxEntries})
		diff := extractDiff(files, err, c.Files, c.Error)
		if diff != "" && countsEveryHeader(input, Limits{MaxBytes: c.Options.MaxBytes, MaxEntries: c.Options.MaxEntries}, err, c.Files, c.Error) {
			counted++
			continue
		}
		if diff != "" {
			t.Errorf("limits %d %+v: %s", i, c.Options, diff)
			failed++
		}
		if c.Error != nil {
			kinds[strings.Fields(c.Error.Message)[1]]++
		}
		if failed > 10 {
			t.FailNow()
		}
	}
	// "Backup archive ...", "Cannot create a Buffer ...", "The value of ..."
	for _, kind := range []string{"archive", "create", "value"} {
		if kinds[kind] < 3 {
			t.Errorf("only %d vectors refused with %q: %v", kinds[kind], kind, kinds)
		}
	}
	t.Logf("%d vectors refused for their entries now (UM43)", counted)
	if counted == 0 {
		t.Error("no vector counts other entries than regular files")
	}
}

// countsEveryHeader says whether Go's refusal err of an archive that
// Node answered with files or wantErr is the intended difference of BUGS.md
// UM43: Node counted the names of regular files against the entry limit,
// Go counts every header, so Go refuses some archives Node took (folders,
// links, repeated names, a fraction as the limit). It is when Go refused
// for the entries and answers as Node did without an entry limit.
func countsEveryHeader(input []byte, limits Limits, err error, files [][2]string, wantErr *jsErr) bool {
	inForce := limits.orDefault()
	if err == nil || err.Error() != "Backup archive has more than "+jsbind.NumberString(inForce.MaxEntries)+" entries" {
		return false
	}
	inForce.MaxEntries = math.Inf(1)
	unlimited, uerr := ExtractTarGz(input, inForce)
	return extractDiff(unlimited, uerr, files, wantErr) == ""
}

// TestEnvLimitsMatchNode: BACKUP_MAX_DECOMPRESSED_BYTES and
// BACKUP_MAX_ENTRIES as Number() reads them, and what the limits they
// give do to the same eight archives.
//
// The intended difference (BUGS.md UM42): where Node took a limit that is
// no whole number from 1 to 2^53-1 (-1, 0.5, Infinity, 1e21), the default
// holds and EnvLimit reports it. Such a case is compared with the Node
// case whose limits are the ones in force now, when there is one.
func TestEnvLimitsMatchNode(t *testing.T) {
	var v archiveVectors
	loadVectors(t, "archive.json.gz", &v)
	if len(v.Env.Cases) < 400 || len(v.Env.Archives) != 8 {
		t.Fatalf("%d env cases over %d archives", len(v.Env.Cases), len(v.Env.Archives))
	}
	type result = struct {
		Files [][2]string `json:"files"`
		Error *jsErr      `json:"error"`
	}
	usable := func(text string) bool {
		n := jsbind.ParseNumber(text)
		return n >= 1 && n <= 1<<53-1 && n == math.Trunc(n)
	}
	nodeResults := map[[2]string][]result{}
	for _, c := range v.Env.Cases {
		if usable(c.MaxBytes) && usable(c.MaxEntries) {
			nodeResults[[2]string{c.MaxBytes, c.MaxEntries}] = c.Results
		}
	}
	failed, changed, compared := 0, 0, 0
	for i, c := range v.Env.Cases {
		env := map[string]string{}
		if c.Bytes != nil {
			env["BACKUP_MAX_DECOMPRESSED_BYTES"] = *c.Bytes
		}
		if c.Entries != nil {
			env["BACKUP_MAX_ENTRIES"] = *c.Entries
		}
		maxBytes, bytesOK := EnvLimit(env["BACKUP_MAX_DECOMPRESSED_BYTES"])
		maxEntries, entriesOK := EnvLimit(env["BACKUP_MAX_ENTRIES"])
		limits := Limits{MaxBytes: maxBytes, MaxEntries: maxEntries}
		inForce := limits.orDefault()

		wantBytes, wantEntries := c.MaxBytes, c.MaxEntries
		if !usable(wantBytes) {
			wantBytes, changed = jsbind.NumberString(DefaultMaxArchiveBytes), changed+1
		}
		if !usable(wantEntries) {
			wantEntries, changed = jsbind.NumberString(DefaultMaxEntries), changed+1
		}
		if got := jsbind.NumberString(inForce.MaxBytes); got != wantBytes {
			t.Errorf("env %d %q: bytes limit %s, want %s (Node %s)", i, env, got, wantBytes, c.MaxBytes)
			failed++
		}
		if got := jsbind.NumberString(inForce.MaxEntries); got != wantEntries {
			t.Errorf("env %d %q: entries limit %s, want %s (Node %s)", i, env, got, wantEntries, c.MaxEntries)
			failed++
		}
		// reported: a value that is set and is not the limit in force
		if want := c.Bytes == nil || jsbind.Trim(*c.Bytes) == "" || usable(*c.Bytes); bytesOK != want {
			t.Errorf("env %d %q: bytes reported %v", i, env, !bytesOK)
			failed++
		}
		if want := c.Entries == nil || jsbind.Trim(*c.Entries) == "" || usable(*c.Entries); entriesOK != want {
			t.Errorf("env %d %q: entries reported %v", i, env, !entriesOK)
			failed++
		}
		want, found := nodeResults[[2]string{wantBytes, wantEntries}]
		if !found {
			continue
		}
		compared++
		for k, w := range want {
			input := unb64(t, v.Env.Archives[k])
			files, err := ExtractTarGz(input, limits)
			if diff := extractDiff(files, err, w.Files, w.Error); diff != "" && !countsEveryHeader(input, limits, err, w.Files, w.Error) {
				t.Errorf("env %d %q archive %d: %s", i, env, k, diff)
				failed++
			}
		}
		if failed > 10 {
			t.FailNow()
		}
	}
	if changed < 100 || compared < 300 {
		t.Errorf("%d limits changed, %d cases compared", changed, compared)
	}
}

func TestTarMatchesNodeByteForByte(t *testing.T) {
	var v archiveVectors
	loadVectors(t, "archive.json.gz", &v)
	if len(v.Tar) < 300 {
		t.Fatalf("only %d tar vectors", len(v.Tar))
	}
	failed := 0
	for i, c := range v.Tar {
		var entries []Entry
		for _, e := range c.Entries {
			mode := int64(0)
			if e.Mode != nil {
				mode = *e.Mode
			}
			entries = append(entries, Entry{Name: e.Name, Data: unb64(t, e.Data), Mode: mode})
		}
		got, err := buildTar(entries, c.Mtime)
		switch {
		case c.Error != nil:
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("tar %d: want error %q, got %v", i, c.Error.Message, err)
				failed++
			}
		case err != nil:
			t.Errorf("tar %d: %v", i, err)
			failed++
		case !bytes.Equal(got, unb64(t, c.Tar)):
			t.Errorf("tar %d: bytes differ (names %q)", i, entries[0].Name)
			failed++
		}
		if failed > 10 {
			t.FailNow()
		}
	}
}

func TestExtractMatchesNode(t *testing.T) {
	var v archiveVectors
	loadVectors(t, "archive.json.gz", &v)
	if len(v.Extract) < 2000 {
		t.Fatalf("only %d extract vectors", len(v.Extract))
	}
	failed := 0
	for i, c := range v.Extract {
		files, err := ExtractTarGz(unb64(t, c.Input), Limits{})
		if c.Error != nil {
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("extract %d: want error %q, got %v", i, c.Error.Message, err)
				failed++
			}
			continue
		}
		if err != nil {
			t.Errorf("extract %d: %v", i, err)
			failed++
			continue
		}
		var got [][2]string
		for _, name := range files.Names() {
			data, _ := files.Get(name)
			got = append(got, [2]string{name, base64.StdEncoding.EncodeToString(data)})
		}
		want := c.Files
		if len(got) != len(want) {
			t.Errorf("extract %d: %d files, want %d: %q", i, len(got), len(want), got)
			failed++
			continue
		}
		for k := range got {
			if got[k] != want[k] {
				t.Errorf("extract %d file %d: got %q, want %q", i, k, got[k][0], want[k][0])
				failed++
				break
			}
		}
		if failed > 10 {
			t.FailNow()
		}
	}
}

func TestGunzipMatchesZlib(t *testing.T) {
	var v archiveVectors
	loadVectors(t, "archive.json.gz", &v)
	if len(v.Gunzip) < 3000 {
		t.Fatalf("only %d gunzip vectors", len(v.Gunzip))
	}
	failed, errors := 0, 0
	for i, c := range v.Gunzip {
		got, err := gunzip(unb64(t, c.Input), DefaultMaxArchiveBytes)
		if c.Error != nil {
			errors++
			if err == nil || err.Error() != c.Error.Message {
				t.Errorf("gunzip %d: want error %q, got %v", i, c.Error.Message, err)
				failed++
			}
		} else if err != nil {
			t.Errorf("gunzip %d: %v", i, err)
			failed++
		} else if !bytes.Equal(got, unb64(t, c.Output)) {
			t.Errorf("gunzip %d: output differs", i)
			failed++
		}
		if failed > 10 {
			t.FailNow()
		}
	}
	if errors < 1000 {
		t.Errorf("only %d of the vectors are errors", errors)
	}
}

type valueVectors struct {
	Validate struct {
		Full []struct {
			Manifest   string   `json:"manifest"`
			NoManifest bool     `json:"noManifest"`
			Broken     string   `json:"broken"`
			Valid      bool     `json:"valid"`
			Errors     []string `json:"errors"`
			Error      *jsErr   `json:"error"`
		} `json:"full"`
		Snapshots []struct {
			Data   json.RawMessage `json:"data"`
			Result *struct {
				Valid  bool     `json:"valid"`
				Errors []string `json:"errors"`
			} `json:"result"`
			Error *jsErr `json:"error"`
		} `json:"snapshots"`
	} `json:"validate"`
	Numbers struct {
		Fixed  [][3]any    `json:"fixed"`
		Base64 [][2]string `json:"base64"`
	} `json:"numbers"`
}

// The checks Go answers otherwise than Node on purpose (BUGS.md BK14):
// Node threw a TypeError for a manifest that is null and for a version
// object with its own toString key; Go answers them as invalid, null like
// any other manifest that is no object, the version as "[object Object]".
var (
	fullCheckDifferences = map[string][]string{
		`null`: {"Archive is not a BetterDesk full backup (wrong _format)", "Unsupported full-backup version: undefined"},
		`{"_format":"betterdesk-full-backup","_version":{"toString":1}}`: {"Unsupported full-backup version: [object Object]"},
	}
	snapshotCheckDifferences = map[string][]string{
		`{"_format":"betterdesk-backup","_version":{"toString":2},"console":{}}`: {"Unsupported backup version: [object Object] (max supported: 1)"},
	}
)

func TestValidationMatchesNode(t *testing.T) {
	var v valueVectors
	loadVectors(t, "values.json.gz", &v)
	s := New(Options{})
	changed := 0
	for i, c := range v.Validate.Full {
		if want, ok := fullCheckDifferences[string(unb64(t, c.Manifest))]; ok && c.Manifest != "" {
			changed++
			c.Valid, c.Errors, c.Error = false, want, nil
		}
		var buf []byte
		switch {
		case c.Broken != "":
			buf = unb64(t, c.Broken)
		case c.NoManifest:
			buf, _ = CreateTarGz([]Entry{{Name: "other", Data: []byte("x")}}, 0)
		default:
			buf, _ = CreateTarGz([]Entry{{Name: "manifest.json", Data: unb64(t, c.Manifest)}}, 0)
		}
		got := s.ValidateFullBackup(buf)
		if c.Error != nil {
			t.Errorf("full %d (%q): Node threw %q", i, unb64(t, c.Manifest), c.Error.Message)
			continue
		}
		if got.Valid != c.Valid || !equalStrings(got.Errors, c.Errors) {
			t.Errorf("full %d (%q): got %v %q, want %v %q", i, unb64(t, c.Manifest), got.Valid, got.Errors, c.Valid, c.Errors)
		}
	}
	for i, c := range v.Validate.Snapshots {
		data, err := branding.ParseJSON(c.Data)
		if err != nil {
			t.Fatal(err)
		}
		if want, ok := snapshotCheckDifferences[branding.Stringify(data)]; ok {
			changed++
			c.Error = nil
			c.Result = &struct {
				Valid  bool     `json:"valid"`
				Errors []string `json:"errors"`
			}{false, want}
		}
		valid, errs := ValidateBackup(data)
		if c.Error != nil {
			t.Errorf("snapshot %d %s: Node threw %q", i, c.Data, c.Error.Message)
			continue
		}
		if valid != c.Result.Valid || !equalStrings(errs, c.Result.Errors) {
			t.Errorf("snapshot %d %s: got %v %q, want %+v", i, c.Data, valid, errs, c.Result)
		}
	}
	if changed != len(fullCheckDifferences)+len(snapshotCheckDifferences) {
		t.Errorf("%d of the differences met a vector", changed)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNumbersAndBase64MatchNode(t *testing.T) {
	var v valueVectors
	loadVectors(t, "values.json.gz", &v)
	if len(v.Numbers.Fixed) < 3000 || len(v.Numbers.Base64) < 2000 {
		t.Fatalf("too few vectors")
	}
	for _, c := range v.Numbers.Fixed {
		n := int64(c[0].(float64))
		if got := ToFixed(n, 1024, 1); got != c[1] {
			t.Errorf("(%d/1024).toFixed(1): got %s, want %s", n, got, c[1])
		}
		if got := ToFixed(n, 1<<20, 2); got != c[2] {
			t.Errorf("(%d/1048576).toFixed(2): got %s, want %s", n, got, c[2])
		}
	}
	for _, c := range v.Numbers.Base64 {
		if got := hex.EncodeToString(nodeBase64(c[0])); got != c[1] {
			t.Errorf("Buffer.from(%q, 'base64'): got %s, want %s", c[0], got, c[1])
		}
	}
}
