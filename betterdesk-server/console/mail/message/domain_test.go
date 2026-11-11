package message

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// addressVector is one line of a file that testdata/nodeoracle/addresses.js
// wrote: an address with what nodemailer normalizes it to, or the hash over
// the results of one block of the code point sweep.
type addressVector struct {
	In     *string `json:"in"`
	Out    string  `json:"out"`
	Block  *int    `json:"block"`
	Count  int     `json:"count"`
	SHA256 string  `json:"sha256"`
}

// readLines calls each for every line of a file of JSON lines, which may be
// gzipped.
func readLines(t testing.TB, file string, each func(line []byte)) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var reader io.Reader = bytes.NewReader(raw)
	if strings.HasSuffix(file, ".gz") {
		if reader, err = gzip.NewReader(reader); err != nil {
			t.Fatalf("open %s: %v", file, err)
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, 64<<20)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
			each(scanner.Bytes())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
}

// sweepTemplates puts a character into the places of an address where the
// handling differs. Keep in step with TEMPLATES in addresses.js.
var sweepTemplates = []func(c string) string{
	func(c string) string { return "a@" + c + ".de" },
	func(c string) string { return "a@x" + c + "y.de" },
	func(c string) string { return "\u00fc@x" + c + "y.de" },
	func(c string) string { return "a@" + c + " x" },
	func(c string) string { return "\u00fc@" + c + " x" },
	func(c string) string { return "\u00fc@a\u03a3" + c + " x" },
	func(c string) string { return "\u00fc@" + c + "\u03a3 x" },
	func(c string) string { return c + "@example.com" },
	func(c string) string { return "a@xn--" + c + ".de" },
	func(c string) string { return "A" + c + "\u00e9@B" + c + ".De" },
}

const sweepBlockSize = 0x1000

// sweepBlock returns the number of addresses of one block of the sweep and
// the hash over their normalized forms.
func sweepBlock(block int) (int, string) {
	t := tables()
	hash := sha256.New()
	count := 0
	for cp := rune(block * sweepBlockSize); cp < rune((block+1)*sweepBlockSize); cp++ {
		if cp >= 0xd800 && cp <= 0xdfff {
			continue
		}
		for _, template := range sweepTemplates {
			io.WriteString(hash, normalizeAddress(t, template(string(cp)))+"\n")
			count++
		}
	}
	return count, hex.EncodeToString(hash.Sum(nil))
}

// replayAddresses compares normalizeAddress with every vector of a file and
// returns how many addresses and sweep blocks it compared.
func replayAddresses(t *testing.T, file string) (addresses, blocks int) {
	tab := tables()
	failed := 0
	readLines(t, file, func(line []byte) {
		var v addressVector
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatalf("decode %s: %v", line, err)
		}
		switch {
		case v.Block != nil:
			blocks++
			count, sum := sweepBlock(*v.Block)
			if count != v.Count || sum != v.SHA256 {
				failed++
				t.Errorf("sweep block %d (U+%04X to U+%04X): %d addresses with hash %s, Node has %d with %s",
					*v.Block, *v.Block*sweepBlockSize, (*v.Block+1)*sweepBlockSize-1, count, sum, v.Count, v.SHA256)
			}
		case v.In != nil:
			addresses++
			if got := normalizeAddress(tab, *v.In); got != v.Out {
				failed++
				t.Errorf("normalizeAddress(%q)\n got  %q\n want %q", *v.In, got, v.Out)
			}
		default:
			t.Fatalf("vector without input: %s", line)
		}
		if failed >= 40 {
			t.Fatalf("stopping after %d failures", failed)
		}
	})
	return addresses, blocks
}

// TestNodeAddressVectors replays what nodemailer's address normalization
// returned: for every code point in ten places of an address (compared by a
// hash per 4096 code points) and for the addresses of
// testdata/nodeoracle/addresses.js in full.
func TestNodeAddressVectors(t *testing.T) {
	addresses, blocks := replayAddresses(t, "testdata/addresses.json.gz")
	if blocks != 0x110000/sweepBlockSize || addresses < 10000 {
		t.Fatalf("%d sweep blocks and %d addresses in testdata/addresses.json.gz, the file is incomplete", blocks, addresses)
	}
	t.Logf("%d addresses, %d sweep blocks of %d addresses", addresses, blocks, sweepBlockSize*len(sweepTemplates))
}

// TestExtraAddressVectors replays a second file of address vectors, named
// by the environment variable MESSAGE_ADDRESS_VECTORS (see
// testdata/nodeoracle/README.md).
func TestExtraAddressVectors(t *testing.T) {
	file := os.Getenv("MESSAGE_ADDRESS_VECTORS")
	if file == "" {
		t.Skip("MESSAGE_ADDRESS_VECTORS is not set")
	}
	addresses, blocks := replayAddresses(t, file)
	t.Logf("%d addresses, %d sweep blocks", addresses, blocks)
}
