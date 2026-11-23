package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// Hand-made deflate streams for the rules of zlib that random input
// rarely reaches.

// bits writes deflate bits: fields lowest bit first, Huffman codes
// highest bit first.
type bits struct {
	out  []byte
	acc  uint32
	nacc uint
}

func (b *bits) field(v uint32, n uint) {
	for i := uint(0); i < n; i++ {
		b.bit((v >> i) & 1)
	}
}

func (b *bits) code(v uint32, n uint) {
	for i := n; i > 0; i-- {
		b.bit((v >> (i - 1)) & 1)
	}
}

func (b *bits) bit(v uint32) {
	b.acc |= v << b.nacc
	b.nacc++
	if b.nacc == 8 {
		b.out = append(b.out, byte(b.acc))
		b.acc, b.nacc = 0, 0
	}
}

func (b *bits) bytes() []byte {
	if b.nacc > 0 {
		return append(b.out, byte(b.acc))
	}
	return b.out
}

// member is a gzip member around a raw deflate body, with a trailer that
// would fit an empty output (the tests end before it counts).
func member(body []byte) []byte {
	head := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 3}
	return append(append(head, body...), 0, 0, 0, 0, 0, 0, 0, 0)
}

func gzipOf(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZlibRules(t *testing.T) {
	// A distance into the member before: each member starts with an
	// empty window.
	var far bits
	far.field(1, 1) // final
	far.field(1, 2) // fixed codes
	far.code(1, 7)  // length 3 (symbol 257)
	far.code(0, 5)  // distance 1
	far.code(0, 7)  // end of block
	twoMembers := append(gzipOf(t, []byte("abc")), member(far.bytes())...)

	// A code length code with one code of one bit: incomplete, which zlib
	// refuses for this code alone.
	var single bits
	single.field(1, 1)
	single.field(2, 2) // dynamic
	single.field(0, 5) // 257 literal/length codes
	single.field(0, 5) // 1 distance code
	single.field(0, 4) // 4 code length codes: 16, 17, 18, 0
	for _, l := range []uint32{0, 0, 0, 1} {
		single.field(l, 3)
	}
	single.field(0, 32)

	// A repeat of the previous length as the first length.
	var repeat bits
	repeat.field(1, 1)
	repeat.field(2, 2)
	repeat.field(0, 5)
	repeat.field(0, 5)
	repeat.field(0, 4)
	for _, l := range []uint32{1, 0, 0, 1} { // symbols 16 and 0, one bit each
		repeat.field(l, 3)
	}
	repeat.code(1, 1) // symbol 16
	repeat.field(0, 32)

	for _, c := range []struct {
		name string
		in   []byte
		want string
	}{
		{"distance before the member", twoMembers, "invalid distance too far back"},
		{"single code length code", member(single.bytes()), "invalid code lengths set"},
		{"repeat first", member(repeat.bytes()), "invalid bit length repeat"},
	} {
		if _, err := gunzip(c.in, DefaultMaxArchiveBytes); err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

// The limit holds for copies too, also when nothing follows them.
func TestGunzipLimitOnCopies(t *testing.T) {
	in := gzipOf(t, bytes.Repeat([]byte("a"), 100000))
	if _, err := gunzip(in, 50000); err == nil || err.Error() != "Cannot create a Buffer larger than 50000 bytes" {
		t.Errorf("copies past the limit: %v", err)
	}
	if out, err := gunzip(in, 100000); err != nil || len(out) != 100000 {
		t.Errorf("at the limit: %d %v", len(out), err)
	}

	// One literal and copies only, so no literal after the limit can
	// catch it: 1 + 4*258 = 1033 bytes of "a".
	var copies bits
	copies.field(1, 1)       // final
	copies.field(1, 2)       // fixed codes
	copies.code(0x30+'a', 8) // literal "a"
	for i := 0; i < 4; i++ {
		copies.code(0xc5, 8) // length 258 (symbol 285)
		copies.code(0, 5)    // distance 1
	}
	copies.code(0, 7) // end of block
	want := bytes.Repeat([]byte("a"), 1033)
	stream := gzipOf(t, want)
	body := append(append([]byte{}, stream[:10]...), copies.bytes()...)
	body = append(body, stream[len(stream)-8:]...) // crc and size of want
	if out, err := gunzip(body, 1033); err != nil || !bytes.Equal(out, want) {
		t.Fatalf("hand-made stream: %d %v", len(out), err)
	}
	if _, err := gunzip(body, 1032); err == nil || err.Error() != "Cannot create a Buffer larger than 1032 bytes" {
		t.Errorf("last copy past the limit: %v", err)
	}
}

// Parts that are not asked for are skipped, whatever the archive holds.
func TestUnchosenPartsStay(t *testing.T) {
	src := newInstall(t, "sqlite")
	src.seed(t)
	archive, _, err := src.service(t, "x").CreateFullBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dst := newInstall(t, "sqlite")
	res, err := dst.service(t, "x").RestoreFullBackup(context.Background(), archive, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 0 || strings.Join(res.Skipped, ",") != "database,uploads,secrets/keys,.env,goserver-db" || res.RequiresRestart {
		t.Errorf("result %+v", res)
	}
	for _, path := range []string{dst.envFile, filepath.Join(dst.keysPath, "id_ed25519"), filepath.Join(dst.dataDir, "uploads")} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s was written", path)
		}
	}
	if rows := snapshot(t, dst.db)["folders"]; len(rows) != 0 {
		t.Errorf("folders %q", rows)
	}
}

// A table whose name is no plain identifier is neither dumped nor
// restored.
func TestTablesWithOddNames(t *testing.T) {
	in := newInstall(t, "sqlite")
	mustExec(t, in.db, `CREATE TABLE "odd-name" (a TEXT)`)
	mustExec(t, in.db, `INSERT INTO "odd-name" VALUES ('in the table')`)
	dump, err := DumpAllTables(context.Background(), in.db)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dump), "odd-name") || !strings.Contains(string(dump), `"folders":[`) {
		t.Errorf("dump %.200s", dump)
	}
	parsed, err := branding.ParseJSON([]byte(`{"tables":{"odd-name":[{"a":"from the dump"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ImportAllTables(context.Background(), in.db, parsed)
	if err != nil || strings.Join(res.Skipped, ",") != "odd-name" || len(res.Restored) != 0 {
		t.Errorf("import %+v %v", res, err)
	}
	if rows := snapshot(t, in.db)["odd-name"]; strings.Join(rows, ",") != "'in the table'" {
		t.Errorf("odd-name %q", rows)
	}
}
