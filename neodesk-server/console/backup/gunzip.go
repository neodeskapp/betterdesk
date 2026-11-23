package backup

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// maxBufferLength is the largest Buffer of Node 22 (buffer.constants.MAX_LENGTH),
// the most zlib's maxOutputLength takes.
const maxBufferLength = 1<<53 - 1

// gunzip decompresses a gzip file as Node's zlib.gunzipSync does, with the
// messages zlib gives for broken input:
//
//   - The members of the file are decompressed one after the other. After
//     a member the rest is ignored when it starts with a zero byte (zlib
//     treats trailing zeros as padding, and stops there); anything else
//     has to be another member.
//   - Input that ends early is "unexpected end of file". Every other
//     failure has the text zlib set: "incorrect header check", "invalid
//     distance too far back", "incorrect data check" and the rest.
//
// limit caps the output; past it the error is the RangeError Node throws
// for a Buffer that is too large.
func gunzip(data []byte, limit int) ([]byte, error) {
	return inflate(data, limit, strconv.Itoa(limit))
}

// gunzipMax is zlib.gunzipSync(data, {maxOutputLength: maxBytes}). zlib
// checks the option first: an infinite one, and one below 1 or above
// the largest Buffer, is a RangeError before any input is read. Then an
// output longer than maxBytes stops it. maxBytes may have a fraction (the
// environment gives one); the messages write it as JavaScript does.
func gunzipMax(data []byte, maxBytes float64) ([]byte, error) {
	switch {
	case math.IsInf(maxBytes, 0):
		return nil, optionOutOfRange("a finite number", maxBytes)
	case maxBytes < 1 || maxBytes > maxBufferLength:
		return nil, optionOutOfRange(">= 1 and <= "+strconv.FormatInt(maxBufferLength, 10), maxBytes)
	}
	limit := math.MaxInt
	if whole := math.Floor(maxBytes); whole < math.MaxInt {
		limit = int(whole)
	}
	return inflate(data, limit, jsbind.NumberString(maxBytes))
}

// optionOutOfRange is Node's ERR_OUT_OF_RANGE for maxOutputLength. It
// writes a whole number beyond 2^32 with "_" between groups of three
// digits, counted from the end of its text.
func optionOutOfRange(want string, received float64) error {
	text := jsbind.NumberString(received)
	if !math.IsInf(received, 0) && received == math.Trunc(received) && math.Abs(received) > 1<<32 {
		text = numericSeparators(text)
	}
	return errors.New(`The value of "options.maxOutputLength" is out of range. It must be ` + want + ". Received " + text)
}

// numericSeparators is addNumericalSeparator of Node's errors.
func numericSeparators(val string) string {
	start := 0
	if strings.HasPrefix(val, "-") {
		start = 1
	}
	res := ""
	i := len(val)
	for ; i >= start+4; i -= 3 {
		res = "_" + val[i-3:i] + res
	}
	return val[:i] + res
}

// inflate decompresses data with at most limit bytes of output; text is
// the limit as the RangeError names it.
func inflate(data []byte, limit int, text string) ([]byte, error) {
	in := &inflater{src: data, limit: limit, limitText: text}
	for {
		if err := in.member(); err != nil {
			return nil, err
		}
		if in.pos >= len(in.src) || in.src[in.pos] == 0 {
			return in.out, nil
		}
	}
}

// zlibError is an error with the text zlib gave it.
type zlibError string

func (e zlibError) Error() string { return string(e) }

// errUnexpectedEOF is what Node reports when the input ends before the
// gzip stream does.
const errUnexpectedEOF = zlibError("unexpected end of file")

// errTooLarge is Node's RangeError for a result above the limit; it holds
// the limit as the message writes it.
type errTooLarge string

func (e errTooLarge) Error() string {
	return "Cannot create a Buffer larger than " + string(e) + " bytes"
}

// inflater holds the state of one decompression: the input with its bit
// position and the output so far.
type inflater struct {
	src       []byte
	pos       int    // next byte of src
	bits      uint64 // bits read ahead, lowest first
	nbits     uint
	out       []byte
	limit     int
	limitText string
	// start is where the current member's output begins; distances may not
	// reach before it.
	start int
}

// byteAt reads one whole byte of the header or trailer.
func (z *inflater) byteAt() (byte, error) {
	if z.pos >= len(z.src) {
		return 0, errUnexpectedEOF
	}
	b := z.src[z.pos]
	z.pos++
	return b, nil
}

func (z *inflater) take(n int) ([]byte, error) {
	if len(z.src)-z.pos < n {
		return nil, errUnexpectedEOF
	}
	b := z.src[z.pos : z.pos+n]
	z.pos += n
	return b, nil
}

// member decompresses one gzip member at z.pos.
func (z *inflater) member() error {
	z.bits, z.nbits = 0, 0
	z.start = len(z.out)
	header := z.pos
	magic, err := z.take(2)
	if err != nil {
		return err
	}
	if magic[0] != 0x1f || magic[1] != 0x8b {
		return zlibError("incorrect header check")
	}
	mf, err := z.take(2)
	if err != nil {
		return err
	}
	if mf[0] != 8 {
		return zlibError("unknown compression method")
	}
	flags := mf[1]
	if flags&0xe0 != 0 {
		return zlibError("unknown header flags set")
	}
	if _, err := z.take(6); err != nil { // time, extra flags, system
		return err
	}
	if flags&0x04 != 0 {
		xlen, err := z.take(2)
		if err != nil {
			return err
		}
		if _, err := z.take(int(binary.LittleEndian.Uint16(xlen))); err != nil {
			return err
		}
	}
	for _, flag := range []byte{0x08, 0x10} { // file name, comment
		if flags&flag == 0 {
			continue
		}
		for {
			b, err := z.byteAt()
			if err != nil {
				return err
			}
			if b == 0 {
				break
			}
		}
	}
	if flags&0x02 != 0 {
		sum := crc32.ChecksumIEEE(z.src[header:z.pos])
		hcrc, err := z.take(2)
		if err != nil {
			return err
		}
		if binary.LittleEndian.Uint16(hcrc) != uint16(sum) {
			return zlibError("header crc mismatch")
		}
	}

	if err := z.inflate(); err != nil {
		return err
	}

	// The trailer starts at the next whole byte. zlib checks the CRC as
	// soon as it has it, before it reads the length.
	z.alignByte()
	produced := z.out[z.start:]
	sum, err := z.take(4)
	if err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(sum) != crc32.ChecksumIEEE(produced) {
		return zlibError("incorrect data check")
	}
	size, err := z.take(4)
	if err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(size) != uint32(len(produced)) {
		return zlibError("incorrect length check")
	}
	return nil
}

// need makes sure n bits (n <= 32) are read ahead.
func (z *inflater) need(n uint) bool {
	for z.nbits < n {
		if z.pos >= len(z.src) {
			return false
		}
		z.bits |= uint64(z.src[z.pos]) << z.nbits
		z.pos++
		z.nbits += 8
	}
	return true
}

func (z *inflater) getBits(n uint) (uint32, error) {
	if !z.need(n) {
		return 0, errUnexpectedEOF
	}
	v := uint32(z.bits & (1<<n - 1))
	z.bits >>= n
	z.nbits -= n
	return v, nil
}

// alignByte drops the bits up to the next byte and gives the unused whole
// bytes of the read-ahead back to the input.
func (z *inflater) alignByte() {
	drop := z.nbits % 8
	z.bits >>= drop
	z.nbits -= drop
	z.pos -= int(z.nbits / 8)
	z.bits, z.nbits = 0, 0
}

// The length and distance bases and extra bits of RFC 1951.
var (
	lengthBase  = [29]uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lengthExtra = [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distBase    = [30]uint32{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distExtra   = [30]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	// codeOrder is the order of the code length code lengths.
	codeOrder = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
)

// huffman is a decoding table: entry i is the code whose bits, read
// lowest first, are the low bits of i. An entry holds the symbol, its
// length in bits and whether it is a code at all.
type huffman struct {
	table []huffEntry
	bits  uint
}

type huffEntry struct {
	sym     uint16
	len     uint8
	invalid bool
}

type tableKind int

const (
	codesTable tableKind = iota // the code length code
	lensTable
	distsTable
)

var errBadTable = errors.New("bad table")

// buildHuffman builds a table from code lengths with zlib's rules
// (inflate_table): an over-subscribed set is an error; an incomplete one
// too, except for a literal or distance code with a single code of one
// bit. A set without any code gives a table whose entries are invalid
// codes of one bit, so that only using it fails.
func buildHuffman(lens []uint8, kind tableKind) (*huffman, error) {
	var count [16]int
	for _, l := range lens {
		count[l]++
	}
	maxLen := uint(15)
	for maxLen >= 1 && count[maxLen] == 0 {
		maxLen--
	}
	if maxLen == 0 {
		return &huffman{table: []huffEntry{{len: 1, invalid: true}, {len: 1, invalid: true}}, bits: 1}, nil
	}
	left := 1
	for l := 1; l <= 15; l++ {
		left <<= 1
		left -= count[l]
		if left < 0 {
			return nil, errBadTable
		}
	}
	if left > 0 && (kind == codesTable || maxLen != 1) {
		return nil, errBadTable
	}
	// canonical codes: the first code of each length
	var next [16]uint32
	code := uint32(0)
	count[0] = 0
	for l := 1; l <= 15; l++ {
		code = (code + uint32(count[l-1])) << 1
		next[l] = code
	}
	h := &huffman{table: make([]huffEntry, 1<<maxLen), bits: maxLen}
	for i := range h.table {
		h.table[i] = huffEntry{len: 1, invalid: true}
	}
	for sym, l := range lens {
		if l == 0 {
			continue
		}
		c := next[l]
		next[l]++
		// reverse the l bits of c: Huffman codes are sent highest bit first
		rev := uint32(0)
		for i := uint8(0); i < l; i++ {
			rev = rev<<1 | (c>>i)&1
		}
		for i := rev; i < uint32(len(h.table)); i += 1 << l {
			h.table[i] = huffEntry{sym: uint16(sym), len: l}
		}
	}
	return h, nil
}

// decode reads one symbol. An entry that is no code reports invalid with
// ok true; input that ends inside a code is errUnexpectedEOF.
func (z *inflater) decode(h *huffman) (sym uint16, invalid bool, err error) {
	if !z.need(h.bits) {
		// Near the end: look the code up in the bits there are.
		e := h.table[z.bits&(1<<h.bits-1)]
		if uint(e.len) > z.nbits {
			return 0, false, errUnexpectedEOF
		}
		z.bits >>= e.len
		z.nbits -= uint(e.len)
		return e.sym, e.invalid, nil
	}
	e := h.table[z.bits&(1<<h.bits-1)]
	z.bits >>= e.len
	z.nbits -= uint(e.len)
	return e.sym, e.invalid, nil
}

var fixedLens, fixedDists *huffman

func init() {
	lens := make([]uint8, 288)
	for i := range lens {
		switch {
		case i < 144:
			lens[i] = 8
		case i < 256:
			lens[i] = 9
		case i < 280:
			lens[i] = 7
		default:
			lens[i] = 8
		}
	}
	fixedLens, _ = buildHuffman(lens, lensTable)
	dists := make([]uint8, 32)
	for i := range dists {
		dists[i] = 5
	}
	fixedDists, _ = buildHuffman(dists, distsTable)
}

// inflate decompresses the deflate data of one member.
func (z *inflater) inflate() error {
	for {
		final, err := z.getBits(1)
		if err != nil {
			return err
		}
		kind, err := z.getBits(2)
		if err != nil {
			return err
		}
		switch kind {
		case 0:
			err = z.stored()
		case 1:
			err = z.codes(fixedLens, fixedDists)
		case 2:
			var lens, dists *huffman
			if lens, dists, err = z.dynamicTables(); err == nil {
				err = z.codes(lens, dists)
			}
		default:
			return zlibError("invalid block type")
		}
		if err != nil {
			return err
		}
		if final == 1 {
			return nil
		}
	}
}

func (z *inflater) stored() error {
	z.alignByte()
	head, err := z.take(4)
	if err != nil {
		return err
	}
	n := binary.LittleEndian.Uint16(head[:2])
	if n != ^binary.LittleEndian.Uint16(head[2:]) {
		return zlibError("invalid stored block lengths")
	}
	data, err := z.take(int(n))
	if err != nil {
		return err
	}
	return z.emit(data...)
}

func (z *inflater) emit(b ...byte) error {
	if len(z.out)+len(b) > z.limit {
		return errTooLarge(z.limitText)
	}
	z.out = append(z.out, b...)
	return nil
}

func (z *inflater) dynamicTables() (*huffman, *huffman, error) {
	hlit, err := z.getBits(5)
	if err != nil {
		return nil, nil, err
	}
	hdist, err := z.getBits(5)
	if err != nil {
		return nil, nil, err
	}
	hclen, err := z.getBits(4)
	if err != nil {
		return nil, nil, err
	}
	nlen, ndist, ncode := int(hlit)+257, int(hdist)+1, int(hclen)+4
	if nlen > 286 || ndist > 30 {
		return nil, nil, zlibError("too many length or distance symbols")
	}
	var codeLens [19]uint8
	for i := 0; i < ncode; i++ {
		v, err := z.getBits(3)
		if err != nil {
			return nil, nil, err
		}
		codeLens[codeOrder[i]] = uint8(v)
	}
	codes, err := buildHuffman(codeLens[:], codesTable)
	if err != nil {
		return nil, nil, zlibError("invalid code lengths set")
	}
	lens := make([]uint8, nlen+ndist)
	for have := 0; have < nlen+ndist; {
		// zlib reads this table without looking at the entry's validity:
		// a code length code without codes reads every bit as length 0.
		sym, _, err := z.decode(codes)
		if err != nil {
			return nil, nil, err
		}
		if sym < 16 {
			lens[have] = uint8(sym)
			have++
			continue
		}
		var length uint8
		var repeat uint32
		switch sym {
		case 16:
			if have == 0 {
				return nil, nil, zlibError("invalid bit length repeat")
			}
			length = lens[have-1]
			n, err := z.getBits(2)
			if err != nil {
				return nil, nil, err
			}
			repeat = 3 + n
		case 17:
			n, err := z.getBits(3)
			if err != nil {
				return nil, nil, err
			}
			repeat = 3 + n
		default:
			n, err := z.getBits(7)
			if err != nil {
				return nil, nil, err
			}
			repeat = 11 + n
		}
		if have+int(repeat) > nlen+ndist {
			return nil, nil, zlibError("invalid bit length repeat")
		}
		for ; repeat > 0; repeat-- {
			lens[have] = length
			have++
		}
	}
	if lens[256] == 0 {
		return nil, nil, zlibError("invalid code -- missing end-of-block")
	}
	litlen, err := buildHuffman(lens[:nlen], lensTable)
	if err != nil {
		return nil, nil, zlibError("invalid literal/lengths set")
	}
	dists, err := buildHuffman(lens[nlen:], distsTable)
	if err != nil {
		return nil, nil, zlibError("invalid distances set")
	}
	return litlen, dists, nil
}

// codes decodes the symbols of a Huffman block up to its end.
func (z *inflater) codes(lens, dists *huffman) error {
	for {
		sym, invalid, err := z.decode(lens)
		if err != nil {
			return err
		}
		if invalid || sym > 285 {
			return zlibError("invalid literal/length code")
		}
		if sym < 256 {
			if len(z.out) >= z.limit {
				return errTooLarge(z.limitText)
			}
			z.out = append(z.out, byte(sym))
			continue
		}
		if sym == 256 {
			return nil
		}
		sym -= 257
		extra, err := z.getBits(uint(lengthExtra[sym]))
		if err != nil {
			return err
		}
		length := int(lengthBase[sym]) + int(extra)
		dsym, invalid, err := z.decode(dists)
		if err != nil {
			return err
		}
		if invalid || dsym > 29 {
			return zlibError("invalid distance code")
		}
		dextra, err := z.getBits(uint(distExtra[dsym]))
		if err != nil {
			return err
		}
		dist := int(distBase[dsym]) + int(dextra)
		if dist > len(z.out)-z.start {
			return zlibError("invalid distance too far back")
		}
		if len(z.out)+length > z.limit {
			return errTooLarge(z.limitText)
		}
		from := len(z.out) - dist
		for i := 0; i < length; i++ {
			z.out = append(z.out, z.out[from+i])
		}
	}
}
