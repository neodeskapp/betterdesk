package backup

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// The archive reader takes uploaded files: it must not panic, not loop,
// and not hand out more than its limit, whatever it is given.

const fuzzLimit = 1 << 20

func fuzzSeeds(f *testing.F) {
	small, _ := CreateTarGz([]Entry{{Name: "manifest.json", Data: []byte(`{"_format":"betterdesk-full-backup","_version":1}`)},
		{Name: "console/uploads/a.png", Data: bytes.Repeat([]byte{7}, 700)}}, 1700000000)
	f.Add(small)
	f.Add(small[:len(small)/2])
	f.Add([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 3})
	f.Add(append(append([]byte{}, small...), small...))
	f.Add(append(append([]byte{}, small...), 0, 'x'))
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(bytes.Repeat([]byte("abc"), 5000))
	_ = zw.Close()
	f.Add(buf.Bytes())
}

func FuzzExtractTarGz(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		done := make(chan struct{})
		var files *Files
		var err error
		go func() {
			defer close(done)
			files, err = ExtractTarGz(data, fuzzLimit)
		}()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatalf("no answer for %d bytes", len(data))
		}
		if err != nil {
			return
		}
		total := 0
		for _, name := range files.Names() {
			d, ok := files.Get(name)
			if !ok {
				t.Fatalf("listed name %q has no data", name)
			}
			total += len(d)
		}
		if total > fuzzLimit {
			t.Fatalf("%d bytes out of an archive limited to %d", total, fuzzLimit)
		}
	})
}

// Where the standard library reads a gzip member to its end and nothing
// follows, the zlib-faithful reader gives the same bytes.
func FuzzGunzipAgreesWithStdlib(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		zr, err := gzip.NewReader(r)
		if err != nil {
			return
		}
		zr.Multistream(false)
		want, err := io.ReadAll(io.LimitReader(zr, fuzzLimit+1))
		if err != nil || r.Len() != 0 || len(want) > fuzzLimit {
			return
		}
		got, err := gunzip(data, fuzzLimit)
		if err != nil {
			t.Fatalf("stdlib reads %d bytes, gunzip fails: %v", len(want), err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("output differs")
		}
	})
}

// Whatever names and data go in, the reader gives back what the writer
// wrote, under the name the writer used.
func FuzzTarRoundTrip(f *testing.F) {
	f.Add("console/uploads/a.png", []byte("x"), "manifest.json", []byte("{}"))
	f.Add(strings.Repeat("p", 150)+"/"+strings.Repeat("n", 99), []byte{}, "\\back\\slash", bytes.Repeat([]byte{0}, 513))
	f.Fuzz(func(t *testing.T, n1 string, d1 []byte, n2 string, d2 []byte) {
		if !validUTF8Name(n1) || !validUTF8Name(n2) {
			return
		}
		entries := []Entry{{Name: n1, Data: d1}, {Name: n2, Data: d2}}
		archive, err := CreateTarGz(entries, 1700000000)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "Archive entry name too long: ") {
				t.Fatal(err)
			}
			return
		}
		for _, e := range entries {
			if !prefixFits(strings.TrimLeft(strings.ReplaceAll(e.Name, `\`, "/"), "/")) {
				return
			}
		}
		files, err := ExtractTarGz(archive, fuzzLimit*4)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string][]byte{}
		var order []string
		for _, e := range entries {
			if e.Name == "" {
				continue
			}
			name := strings.TrimLeft(strings.ReplaceAll(e.Name, `\`, "/"), "/")
			if _, seen := want[name]; !seen {
				order = append(order, name)
			}
			want[name] = e.Data
		}
		if strings.Join(files.Names(), "\x00") != strings.Join(order, "\x00") {
			t.Fatalf("names %q, want %q", files.Names(), order)
		}
		for name, data := range want {
			got, _ := files.Get(name)
			if !bytes.Equal(got, data) {
				t.Fatalf("data of %q differs", name)
			}
		}
	})
}

// validUTF8Name leaves out names the archive cannot carry back as they
// are: broken UTF-8, NUL and line breaks, which the reader cuts at.
func validUTF8Name(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\n\r"+string(rune(0x2028))+string(rune(0x2029)))
}

// prefixFits reports whether a long name splits into a prefix that fits
// its 155 bytes; a longer prefix is cut, as it always was.
func prefixFits(name string) bool {
	if len(name) <= 100 {
		return true
	}
	units := utf16.Encode([]rune(name))
	for i := min(155, len(units)-1); i > 0; i-- {
		if units[i] == '/' {
			return len(string(utf16.Decode(units[:i]))) <= 155
		}
	}
	return true
}
