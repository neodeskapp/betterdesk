package backup

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
)

// The vectors are what the Node console's own modules answered
// (testdata/nodeoracle/vectors.js). Every one is replayed.

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
		files, err := ExtractTarGz(unb64(t, c.Input), DefaultMaxArchiveBytes)
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

func TestValidationMatchesNode(t *testing.T) {
	var v valueVectors
	loadVectors(t, "values.json.gz", &v)
	s := New(Options{})
	for i, c := range v.Validate.Full {
		var buf []byte
		switch {
		case c.Broken != "":
			buf = unb64(t, c.Broken)
		case c.NoManifest:
			buf, _ = CreateTarGz([]Entry{{Name: "other", Data: []byte("x")}}, 0)
		default:
			buf, _ = CreateTarGz([]Entry{{Name: "manifest.json", Data: unb64(t, c.Manifest)}}, 0)
		}
		got, err := s.ValidateFullBackup(buf)
		if c.Error != nil {
			if err == nil || errorText(err) != c.Error.Message {
				t.Errorf("full %d (%q): want thrown %q, got %v", i, unb64(t, c.Manifest), c.Error.Message, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("full %d: %v", i, err)
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
		valid, errs, err := ValidateBackup(data)
		if c.Error != nil {
			if err == nil || errorText(err) != c.Error.Message {
				t.Errorf("snapshot %d: want thrown %q, got %v", i, c.Error.Message, err)
			}
			continue
		}
		if err != nil || valid != c.Result.Valid || !equalStrings(errs, c.Result.Errors) {
			t.Errorf("snapshot %d %s: got %v %q %v, want %+v", i, c.Data, valid, errs, err, c.Result)
		}
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
