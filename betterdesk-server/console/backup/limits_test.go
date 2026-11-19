package backup

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// TestArchiveLimits carries tests/backupArchive.limits.test.js (audit M4):
// a normal backup comes back whole; an archive that unpacks past the byte
// limit, one with more regular files than the entry limit and one whose
// entry runs past the end of the tar file are refused, each with its own
// message.
func TestArchiveLimits(t *testing.T) {
	normal, err := CreateTarGz([]Entry{{Name: "console/data.json", Data: []byte(`{"ok":true}`)}, {Name: "console/.env", Data: []byte("PORT=5000\n")}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(normal, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := files.Get("console/data.json"); string(data) != `{"ok":true}` {
		t.Errorf("data.json %q", data)
	}
	if data, _ := files.Get("console/.env"); string(data) != "PORT=5000\n" {
		t.Errorf(".env %q", data)
	}

	// 4 MiB of zeros are a few KB of gzip
	bomb, err := CreateTarGz([]Entry{{Name: "bomb.bin", Data: make([]byte, 4<<20)}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bomb) >= 64<<10 {
		t.Fatalf("the bomb is %d bytes", len(bomb))
	}
	if _, err := ExtractTarGz(bomb, Limits{MaxBytes: 1 << 20}); err == nil || err.Error() != "Cannot create a Buffer larger than 1048576 bytes" {
		t.Errorf("past the byte limit: %v", err)
	}
	// header, data and the two end blocks
	if _, err := ExtractTarGz(bomb, Limits{MaxBytes: 4<<20 + 3*blockSize}); err != nil {
		t.Errorf("at the byte limit: %v", err)
	}

	var twelve []Entry
	for i := range 12 {
		twelve = append(twelve, Entry{Name: "f-" + string(rune('a'+i)) + ".txt", Data: []byte("x")})
	}
	many, err := CreateTarGz(twelve, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTarGz(many, Limits{MaxEntries: 5}); err == nil || err.Error() != "Backup archive has more than 5 entries" {
		t.Errorf("past the entry limit: %v", err)
	}
	if files, err := ExtractTarGz(many, Limits{MaxEntries: 12}); err != nil || len(files.Names()) != 12 {
		t.Errorf("at the entry limit: %v", err)
	}

	// The header claims 4096 bytes and the archive ends after it
	tar, err := buildTar([]Entry{{Name: "truncated.bin", Data: make([]byte, 4096)}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTarGz(gzipOf(t, tar[:blockSize]), Limits{}); err == nil || err.Error() != "Backup archive entry truncated.bin extends past the end of the archive" {
		t.Errorf("entry past the end: %v", err)
	}
	if DefaultMaxEntries < 1000 || DefaultMaxEntries != 50000 || DefaultMaxArchiveBytes != 2<<30 {
		t.Errorf("defaults %d entries, %d bytes", DefaultMaxEntries, DefaultMaxArchiveBytes)
	}
}

// UM42: a limit from the environment is a whole number from 1 to 2^53-1.
// Anything else gets the default and is reported, so -1 ("no limit"), a
// fraction or Infinity no longer refuse every archive.
func TestEnvLimitTakesWholeNumbers(t *testing.T) {
	archive, err := CreateTarGz([]Entry{{Name: "a", Data: bytes.Repeat([]byte("x"), 10)}, {Name: "b", Data: []byte("y")}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		env   string
		limit float64
		ok    bool
	}{
		{"", 0, true}, {" \t", 0, true},
		{"1", 1, true}, {" 2048 ", 2048, true}, {"0x800", 2048, true}, {"1e3", 1000, true}, {"9007199254740991", 9007199254740991, true},
		{"0", 0, false}, {"-0", 0, false}, {"-1", 0, false}, {"0.5", 0, false}, {"2048.5", 0, false}, {"1.5", 0, false},
		{"Infinity", 0, false}, {"-Infinity", 0, false}, {"1e16", 0, false}, {"1e21", 0, false}, {"9007199254740992", 0, false},
		{"abc", 0, false}, {"2048abc", 0, false}, {"NaN", 0, false},
	} {
		if limit, ok := EnvLimit(c.env); limit != c.limit || ok != c.ok {
			t.Errorf("%q: %v %v, want %v %v", c.env, limit, ok, c.limit, c.ok)
		}
	}
	for _, env := range []string{"-1", "0.5", "Infinity", "1e16", "1e21", "2048.5"} {
		limit, _ := EnvLimit(env)
		if _, err := ExtractTarGz(archive, Limits{MaxBytes: limit, MaxEntries: limit}); err != nil {
			t.Errorf("limits from %s: %v", env, err)
		}
	}
	limit, _ := EnvLimit("0x800")
	if _, err := ExtractTarGz(archive, Limits{MaxBytes: limit}); err == nil || err.Error() != "Cannot create a Buffer larger than 2048 bytes" {
		t.Errorf("0x800: %v", err)
	}
}

// Limits given as they are, as the old module took its options: a
// negative or infinite byte limit refuses every archive with zlib's
// RangeError for the option, a fraction is compared as it is.
func TestOddLimits(t *testing.T) {
	archive, err := CreateTarGz([]Entry{{Name: "a", Data: bytes.Repeat([]byte("x"), 10)}, {Name: "b", Data: []byte("y")}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		limit float64
		want  string
	}{
		{-1, `The value of "options.maxOutputLength" is out of range. It must be >= 1 and <= 9007199254740991. Received -1`},
		{0.5, `The value of "options.maxOutputLength" is out of range. It must be >= 1 and <= 9007199254740991. Received 0.5`},
		{math.Inf(1), `The value of "options.maxOutputLength" is out of range. It must be a finite number. Received Infinity`},
		{1e16, `The value of "options.maxOutputLength" is out of range. It must be >= 1 and <= 9007199254740991. Received 10_000_000_000_000_000`},
		{1e21, `The value of "options.maxOutputLength" is out of range. It must be >= 1 and <= 9007199254740991. Received 1e_+21`},
		{2048.5, `Cannot create a Buffer larger than 2048.5 bytes`},
	} {
		if _, err := ExtractTarGz(archive, Limits{MaxBytes: c.limit}); err == nil || err.Error() != c.want {
			t.Errorf("byte limit %v: %v", c.limit, err)
		}
	}
	if !strings.HasPrefix(numericSeparators("-4294967297"), "-4_294") {
		t.Errorf("separators %s", numericSeparators("-4294967297"))
	}
}
