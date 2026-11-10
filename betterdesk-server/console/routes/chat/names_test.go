package chat

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// nameVector is one line of testdata/names.json.gz: what the Node console
// made of an uploaded file's name (testdata/nodeoracle).
type nameVector struct {
	Name        string          `json:"name"`
	Ext         string          `json:"ext"`
	FileID      string          `json:"fileId"`
	Disposition json.RawMessage `json:"disposition"`
	Type        string          `json:"type"`
}

// latin1Bytes writes a text of characters up to U+00FF with one byte per
// character, the way a header goes over the wire.
func latin1Bytes(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

func TestNameVectors(t *testing.T) {
	file, err := os.Open("testdata/names.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	const id = "0123456789abcdef0123456789abcdef"
	lines := bufio.NewScanner(unzipped)
	count, headers, typed := 0, 0, 0
	unknownTypes := map[string]bool{}
	for lines.Scan() {
		var v nameVector
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		count++
		ext := storedExtension(v.Name)
		if ext != v.Ext {
			t.Errorf("%q: stored with %q, Node has %q", v.Name, ext, v.Ext)
			continue
		}
		stored := id + ext
		if got := strings.TrimSuffix(stored, extname(stored)); got != v.FileID {
			t.Errorf("%q: file id %q, Node has %q", v.Name, got, v.FileID)
		}
		// The type of a download comes from the console's file server,
		// whose table of extensions is shorter than the old console's: an
		// extension it does not know is application/octet-stream there.
		switch got := httpx.StaticContentType(stored); {
		case got == v.Type:
			typed++
		case got == "application/octet-stream":
			unknownTypes[v.Type] = true
		default:
			t.Errorf("%q: type %q, Node has %q", v.Name, got, v.Type)
		}
		var disposition string
		if err := json.Unmarshal(v.Disposition, &disposition); err != nil {
			// Node failed on half a character, which no stored name has
			if !strings.ContainsRune(ext, utf8.RuneError) {
				t.Errorf("%q: Node has no header (%s) for the extension %q", v.Name, v.Disposition, ext)
			}
			continue
		}
		headers++
		if got := contentDisposition(stored); got != latin1Bytes(disposition) {
			t.Errorf("%q: Content-Disposition %q, Node has %q", v.Name, got, latin1Bytes(disposition))
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if len(unknownTypes) > 0 {
		t.Logf("%d types of the old console that the file server does not know: %v", len(unknownTypes), unknownTypes)
	}
	if count < 4000 || headers < count-200 || typed < count-200 {
		t.Fatalf("read %d vectors, %d with a header, %d with the type of the old console: the file has more than 4000, nearly all with both", count, headers, typed)
	}
}

func TestExtname(t *testing.T) {
	cases := []struct{ name, ext, stored string }{
		{"note.txt", ".txt", ".txt"},
		{"archive.tar.gz", ".gz", ".gz"},
		{"README", "", ""},
		{".bashrc", "", ""},
		{".env.local", ".local", ".local"},
		{"a.", ".", "."},
		{"..", "", ""},
		{"a..", ".", "."},
		{"a.verylongextension", ".verylongextension", ".verylonge"},
		{"a.\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", ".\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", ".\U0001F600\U0001F600\U0001F600\U0001F600�"},
		{"dir.d/file", "", ""},
		{"dir/file.x/", ".x", ".x"},
	}
	for _, c := range cases {
		if got := extname(c.name); got != c.ext {
			t.Errorf("extname(%q) = %q, want %q", c.name, got, c.ext)
		}
		if got := storedExtension(c.name); got != c.stored {
			t.Errorf("storedExtension(%q) = %q, want %q", c.name, got, c.stored)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	cases := []struct{ name, header string }{
		{"abc.txt", `attachment; filename="abc.txt"`},
		{"abc", `attachment; filename="abc"`},
		{`abc.a"b\c`, `attachment; filename="abc.a\"b\\c"`},
		{"abc.a b", `attachment; filename="abc.a b"`},
		{"abc.%41", `attachment; filename="abc.%41"; filename*=UTF-8''abc.%2541`},
		{"abc.é", "attachment; filename=\"abc.\xe9\""},
		{"abc.€", `attachment; filename="abc.?"; filename*=UTF-8''abc.%E2%82%AC`},
		{"abc.\U0001F600", `attachment; filename="abc.??"; filename*=UTF-8''abc.%F0%9F%98%80`},
		{"abc.a\tb", `attachment; filename="abc.a?b"; filename*=UTF-8''abc.a%09b`},
		{"abc.\u0085", `attachment; filename="abc.?"; filename*=UTF-8''abc.%C2%85`},
		{"abc.(a)'*~!", `attachment; filename="abc.(a)'*~!"`},
		{"abc.€(a)'*", `attachment; filename="abc.?(a)'*"; filename*=UTF-8''abc.%E2%82%AC%28a%29%27%2A`},
	}
	for _, c := range cases {
		if got := contentDisposition(c.name); got != c.header {
			t.Errorf("contentDisposition(%q) = %q, want %q", c.name, got, c.header)
		}
	}
}

// FuzzNames feeds the name functions arbitrary file names. The extension
// a file is stored with must be short, start with a dot and hold no
// slash, and the download header must be plain enough for a header line.
func FuzzNames(f *testing.F) {
	for _, seed := range []string{"note.txt", "", ".", "..", "a.b/c", "a.\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", "a.%41\"\\", "a.\r\nSet-Cookie: x=1", "a.\x00b", "a.\xff\xfe"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		ext := storedExtension(name)
		if ext != "" && ext[0] != '.' {
			t.Fatalf("%q is stored with %q, which does not start with a dot", name, ext)
		}
		if strings.Contains(ext, "/") {
			t.Fatalf("%q is stored with %q, which has a slash", name, ext)
		}
		if n := len([]rune(ext)); n > 10 {
			t.Fatalf("%q is stored with %q: %d characters", name, ext, n)
		}
		stored := "0123456789abcdef0123456789abcdef" + ext
		if got := strings.TrimSuffix(stored, extname(stored)); got != stored[:32] && utf8.ValidString(name) {
			t.Fatalf("%q: the stored name %q gives the id %q", name, stored, got)
		}
		header := contentDisposition(stored)
		if !strings.HasPrefix(header, "attachment; filename") {
			t.Fatalf("%q: header %q", name, header)
		}
		for i := 0; i < len(header); i++ {
			if header[i] < 0x20 || header[i] == 0x7f {
				t.Fatalf("%q: the header %q has the control character %#x", name, header, header[i])
			}
		}
	})
}
