package backup

import (
	"bytes"
	"compress/gzip"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// The archive is a gzip-compressed tar file in the ustar format with
// regular files only, the format services/backupArchive.js wrote and read.
// The standard tar command extracts it.

const blockSize = 512

// Entry is one file of an archive.
type Entry struct {
	Name string
	Data []byte
	// Mode is the file mode in the header; 0 means 0644.
	Mode int64
}

// Files is the content of an archive: the files by name, in the order
// their names first appeared. A name that appears twice keeps its first
// place and the data of its last entry, as a JavaScript Map did.
type Files struct {
	names []string
	data  map[string][]byte
}

func newFiles() *Files { return &Files{data: map[string][]byte{}} }

func (f *Files) set(name string, data []byte) {
	if _, ok := f.data[name]; !ok {
		f.names = append(f.names, name)
	}
	f.data[name] = data
}

// Get returns the data of a file and whether the archive has it.
func (f *Files) Get(name string) ([]byte, bool) {
	data, ok := f.data[name]
	return data, ok
}

// Has reports whether the archive has a file of that name.
func (f *Files) Has(name string) bool {
	_, ok := f.data[name]
	return ok
}

// Names lists the file names in order.
func (f *Files) Names() []string { return f.names }

// CreateTarGz packs the entries into a gzip-compressed tar file. mtime is
// the modification time of every file, in seconds. An entry without a name
// is left out. A name longer than 100 bytes is split at a slash into the
// ustar prefix and name; one that does not fit is an error.
func CreateTarGz(entries []Entry, mtime int64) ([]byte, error) {
	tar, err := buildTar(entries, mtime)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	// zlib on Unix names the system as Unix; Go would say "unknown".
	zw.OS = 3
	if _, err := zw.Write(tar); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// buildTar is the tar file before compression.
func buildTar(entries []Entry, mtime int64) ([]byte, error) {
	var tar bytes.Buffer
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		name := strings.TrimLeft(strings.ReplaceAll(e.Name, `\`, "/"), "/")
		mode := e.Mode
		if mode == 0 {
			mode = 0o644
		}
		header, err := buildHeader(name, int64(len(e.Data)), mode, mtime)
		if err != nil {
			return nil, err
		}
		tar.Write(header)
		tar.Write(e.Data)
		if pad := padToBlock(int64(len(e.Data))); pad > 0 {
			tar.Write(make([]byte, pad))
		}
	}
	tar.Write(make([]byte, 2*blockSize))
	return tar.Bytes(), nil
}

// buildHeader builds the 512-byte ustar header of a regular file.
func buildHeader(name string, size, mode, mtime int64) ([]byte, error) {
	header := make([]byte, blockSize)
	filename, prefix := name, ""
	if len(filename) > 100 {
		// The split point is found in UTF-16 units, as JavaScript's
		// lastIndexOf('/', 155) found it.
		units := utf16.Encode([]rune(filename))
		start := 155
		if start > len(units)-1 {
			start = len(units) - 1
		}
		for i := start; i > 0; i-- {
			if units[i] == '/' {
				prefix = string(utf16.Decode(units[:i]))
				filename = string(utf16.Decode(units[i+1:]))
				break
			}
		}
	}
	if len(filename) > 100 {
		return nil, errors.New("Archive entry name too long: " + name)
	}
	copy(header[0:100], filename)
	writeOctal(header, mode&0o7777, 100, 8)
	writeOctal(header, 0, 108, 8)
	writeOctal(header, 0, 116, 8)
	writeOctal(header, size, 124, 12)
	writeOctal(header, mtime, 136, 12)
	copy(header[148:156], "        ")
	header[156] = '0'
	copy(header[257:], "ustar\x00")
	copy(header[263:], "00")
	if prefix != "" {
		copy(header[345:500], truncateUTF8(prefix, 155))
	}
	sum := 0
	for _, b := range header {
		sum += int(b)
	}
	chk := strconv.FormatInt(int64(sum), 8)
	chk = strings.Repeat("0", max(0, 6-len(chk))) + chk
	copy(header[148:154], chk[len(chk)-6:])
	header[154] = 0
	header[155] = ' '
	return header, nil
}

// writeOctal writes value as length-1 octal digits, zero padded, and a
// NUL. Digits that do not fit are cut off at the front.
func writeOctal(buf []byte, value int64, offset, length int) {
	s := strconv.FormatInt(value, 8)
	if len(s) < length-1 {
		s = strings.Repeat("0", length-1-len(s)) + s
	}
	copy(buf[offset:offset+length-1], s[len(s)-(length-1):])
	buf[offset+length-1] = 0
}

// truncateUTF8 cuts s to at most n bytes without splitting a character,
// as Buffer.write did with a length.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := 0
	for i := range s {
		if i > n {
			break
		}
		cut = i
	}
	return s[:cut]
}

// padToBlock is the padding after size bytes of data: JavaScript's
// size % 512, which keeps the sign of size.
func padToBlock(size int64) int64 {
	rem := size % blockSize
	if rem == 0 {
		return 0
	}
	return blockSize - rem
}

// ErrNoProgress is returned for an archive whose size fields lead the
// reader back to a header it has read already. The old console read that
// header again and again, for ever (BUGS.md BK4).
var ErrNoProgress = errors.New("tar entry size leads back to a header read before")

// The limits of an archive that is read, by default.
const (
	// DefaultMaxArchiveBytes is the largest unpacked tar file: 2 GiB.
	DefaultMaxArchiveBytes = 2 << 30
	// DefaultMaxEntries is the most entries (headers of any type) an
	// archive may hold.
	DefaultMaxEntries = 50000
)

// Limits cap what ExtractTarGz takes from an archive. The console fills
// them with EnvLimit from BACKUP_MAX_DECOMPRESSED_BYTES and
// BACKUP_MAX_ENTRIES, so a field is a whole number from 1 to 2^53-1, or 0
// (or NaN) for the default. Other values are taken as they are, fractions,
// negative and infinite ones included.
type Limits struct {
	// MaxBytes caps the unpacked tar file (zlib's maxOutputLength). Below
	// 1, above the largest Buffer (2^53-1) and infinite, every archive is
	// refused with zlib's RangeError for the option.
	MaxBytes float64
	// MaxEntries caps the entries: the header that makes more than
	// MaxEntries is refused, whatever its type and name.
	MaxEntries float64
}

// orDefault is the limits with the defaults for 0 and NaN.
func (l Limits) orDefault() Limits {
	if l.MaxBytes == 0 || math.IsNaN(l.MaxBytes) {
		l.MaxBytes = DefaultMaxArchiveBytes
	}
	if l.MaxEntries == 0 || math.IsNaN(l.MaxEntries) {
		l.MaxEntries = DefaultMaxEntries
	}
	return l
}

// maxSafeInteger is 2^53-1, the largest limit EnvLimit takes.
const maxSafeInteger = 1<<53 - 1

// EnvLimit reads the environment variable of a limit
// (BACKUP_MAX_DECOMPRESSED_BYTES, BACKUP_MAX_ENTRIES) as JavaScript's
// Number() reads text, so " 2048 ", "1e3" and "0x800" are numbers. A whole
// number from 1 to 2^53-1 is the limit. Unset or blank is 0, the default.
// Anything else (text that is no number, 0, a negative number, a
// fraction, Infinity, a number past 2^53-1) is 0 too, with ok false, so
// the caller can say that the default holds instead.
func EnvLimit(value string) (limit float64, ok bool) {
	if jsbind.Trim(value) == "" {
		return 0, true
	}
	n := jsbind.ParseNumber(value)
	if math.IsNaN(n) || n < 1 || n > maxSafeInteger || n != math.Trunc(n) {
		return 0, false
	}
	return n, true
}

// ExtractTarGz unpacks an archive made by CreateTarGz, as the old console
// read it: up to the first block of zeros, regular files only (type '0'
// or NUL), names cut at the first NUL that no line break follows, a size
// field read with parseInt in base 8 after its spaces and NULs are
// removed. Within the limits: an archive that unpacks to more than
// MaxBytes is zlib's RangeError, an entry beyond MaxEntries is "Backup
// archive has more than <n> entries" (every header counts: folders, links
// and repeated names too), and an entry whose size
// runs past the end of the tar file is "Backup archive entry <name>
// extends past the end of the archive".
func ExtractTarGz(buf []byte, limits Limits) (*Files, error) {
	limits = limits.orDefault()
	tar, err := gunzipMax(buf, limits.MaxBytes)
	if err != nil {
		return nil, err
	}
	files := newFiles()
	n := int64(len(tar))
	// A negative size moves the offset back; past the start, offsets count
	// from the end of the tar file as in JavaScript, and a header may come
	// out short. Coming back to an offset read before would repeat for
	// ever, so it ends the read.
	seen := map[int64]bool{}
	offset := int64(0)
	entries := 0
	for offset+blockSize <= n {
		if seen[offset] {
			return nil, ErrNoProgress
		}
		seen[offset] = true
		hs, he := subarray(n, offset, offset+blockSize)
		header := tar[hs:he]
		if allZero(header) {
			break
		}
		name := cutAtNUL(nodeUTF8(field(header, 0, 100)))
		if prefix := cutAtNUL(nodeUTF8(field(header, 345, 500))); prefix != "" {
			name = prefix + "/" + name
		}
		entries++
		if float64(entries) > limits.MaxEntries {
			return nil, errors.New("Backup archive has more than " + jsbind.NumberString(limits.MaxEntries) + " entries")
		}
		size := parseOctal(field(header, 124, 136))
		// A header too short for a type counts as a regular file ('').
		regular := true
		if t := field(header, 156, 157); len(t) == 1 {
			regular = t[0]&0x7f == '0' || t[0]&0x7f == 0
		}
		offset += blockSize
		if offset+size > n {
			return nil, errors.New("Backup archive entry " + name + " extends past the end of the archive")
		}
		from, to := subarray(n, offset, offset+size)
		data := tar[from:to]
		offset += size + padToBlock(size)
		if regular {
			files.set(name, bytes.Clone(data))
		}
	}
	return files, nil
}

// field is buf.toString(enc, start, end) of a header that may be short:
// the bytes from start to end that exist.
func field(b []byte, start, end int) []byte {
	if start > len(b) {
		return nil
	}
	return b[start:min(end, len(b))]
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// subarray is the range Buffer.subarray(start, end) takes from a buffer
// of length n: negative ends count from the end, and a range that ends
// before it starts is empty.
func subarray(n, start, end int64) (int, int) {
	clamp := func(i int64) int64 {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	s, e := clamp(start), clamp(end)
	if e < s {
		e = s
	}
	return int(s), int(e)
}

// cutAtNUL is s.replace(/\0.*$/, "") in JavaScript, whose dot does not
// match a line break: the cut is at the first NUL after the last line
// break.
func cutAtNUL(s string) string {
	from := 0
	for i, r := range s {
		if r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 {
			from = i + utf8.RuneLen(r)
		}
	}
	if i := strings.IndexByte(s[from:], 0); i >= 0 {
		return s[:from+i]
	}
	return s
}

// parseOctal reads a number field: its bytes as 7-bit ASCII, without
// spaces and NULs, through parseInt(text, 8); NaN counts as 0.
func parseOctal(field []byte) int64 {
	var b strings.Builder
	for _, c := range field {
		c &= 0x7f
		if c == 0 || c == ' ' {
			continue
		}
		b.WriteByte(c)
	}
	s := strings.TrimLeft(b.String(), "\t\n\v\f\r")
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	var v int64
	digits := 0
	for _, c := range []byte(s) {
		if c < '0' || c > '7' {
			break
		}
		v = v*8 + int64(c-'0')
		digits++
	}
	if digits == 0 {
		return 0
	}
	if negative {
		return -v
	}
	return v
}

// nodeUTF8 decodes bytes as Node's Buffer.toString('utf8') does: each
// broken sequence becomes one U+FFFD (the WHATWG decoder).
func nodeUTF8(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	needed, seen := 0, 0
	var cp rune
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c < 0x80:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				out.WriteRune(0xFFFD)
			}
			continue
		}
		if c < lower || c > upper {
			needed, seen, cp = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(0xFFFD)
			i--
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			out.WriteRune(cp)
			needed, seen, cp = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(0xFFFD)
	}
	return out.String()
}
