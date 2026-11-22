package message

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"sync"
)

// tables.bin.gz holds the Unicode data the domain handling needs: the IDNA
// mapping, normalization, text direction and joining data of the URL library
// inside Node 22 (ada 2.9.2, Unicode 15.0), and the lower-case mapping of the
// JavaScript engine (Unicode 17.0). It is a fixed recording of the old
// console and cannot be made again.
//
//go:embed tables.bin.gz
var tablesFile []byte

// unicodeTables is the decoded content of tables.bin.gz. Nothing writes to
// it after loading.
type unicodeTables struct {
	// mapRanges holds pairs (first code point, descriptor), sorted. A pair
	// covers every code point up to the next pair. The low byte of the
	// descriptor says what happens to the code point: 0 dropped, 1 kept,
	// 2 not allowed, 3 replaced by descriptor>>24 values of mapData starting
	// at index (descriptor>>8)&0xffff.
	mapRanges []uint32
	mapData   []uint32

	// The normalization data is in pages of 256 code points: an index gives
	// the page of a code point, a page row is 257 wide where a value is read
	// together with its right neighbour.
	decompIndex []uint8
	decompBlock []uint16
	decompData  []uint32
	cccIndex    []uint8
	cccBlock    []uint8
	compIndex   []uint8
	compBlock   []uint16
	compData    []uint32

	// bidi holds triples (first, last, direction), sorted.
	bidi []uint32
	// combining lists the code points a label must not start with, sorted.
	combining []uint32
	// virama, joinR, joinL and joinD are sorted lists for the rules about
	// the zero width joiners.
	virama []uint32
	joinR  []uint32
	joinL  []uint32
	joinD  []uint32

	// lower maps a code point to what String.prototype.toLowerCase makes of
	// it on its own, for the code points it changes.
	lower map[rune][]rune
	// caseClass holds triples (first, last, class), sorted: class 1 is a
	// cased letter, class 2 a character that is skipped when looking for one.
	caseClass []uint32
}

// tables returns the decoded Unicode data. The first call unpacks it.
var tables = sync.OnceValue(func() *unicodeTables {
	t, err := loadTables(tablesFile)
	if err != nil {
		panic("message: embedded tables.bin.gz: " + err.Error())
	}
	return t
})

const tablesMagic = "BDMSGTAB1\n"

// loadTables unpacks a table file: a magic line, then sections of a name
// (one length byte and the name), the size of a value in bytes, the number
// of values as 32 bits, and the values, all little endian.
func loadTables(file []byte) (*unicodeTables, error) {
	reader, err := gzip.NewReader(bytes.NewReader(file))
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("unpack: %w", err)
	}
	if !bytes.HasPrefix(raw, []byte(tablesMagic)) {
		return nil, fmt.Errorf("not a table file")
	}
	raw = raw[len(tablesMagic):]

	t := &unicodeTables{}
	var lower []uint32
	wide := map[string]*[]uint32{
		"map_ranges": &t.mapRanges, "map_data": &t.mapData, "decomp_data": &t.decompData,
		"comp_data": &t.compData, "bidi": &t.bidi, "combining": &t.combining, "virama": &t.virama,
		"join_r": &t.joinR, "join_l": &t.joinL, "join_d": &t.joinD, "lower": &lower, "case_class": &t.caseClass,
	}
	medium := map[string]*[]uint16{"decomp_block": &t.decompBlock, "comp_block": &t.compBlock}
	narrow := map[string]*[]uint8{"decomp_index": &t.decompIndex, "ccc_index": &t.cccIndex, "ccc_block": &t.cccBlock, "comp_index": &t.compIndex}
	seen := 0
	for len(raw) > 0 {
		nameLen := int(raw[0])
		if len(raw) < 1+nameLen+5 {
			return nil, fmt.Errorf("cut off in a section header")
		}
		name := string(raw[1 : 1+nameLen])
		size := int(raw[1+nameLen])
		count := int(binary.LittleEndian.Uint32(raw[2+nameLen:]))
		raw = raw[6+nameLen:]
		if size != 1 && size != 2 && size != 4 || count > len(raw)/size {
			return nil, fmt.Errorf("section %q: bad size", name)
		}
		data := raw[:count*size]
		raw = raw[count*size:]
		switch {
		case size == 4 && wide[name] != nil:
			values := make([]uint32, count)
			for i := range values {
				values[i] = binary.LittleEndian.Uint32(data[i*4:])
			}
			*wide[name] = values
		case size == 2 && medium[name] != nil:
			values := make([]uint16, count)
			for i := range values {
				values[i] = binary.LittleEndian.Uint16(data[i*2:])
			}
			*medium[name] = values
		case size == 1 && narrow[name] != nil:
			*narrow[name] = bytes.Clone(data)
		default:
			return nil, fmt.Errorf("section %q: not expected", name)
		}
		seen++
	}
	if seen != len(wide)+len(medium)+len(narrow) {
		return nil, fmt.Errorf("%d sections, want %d", seen, len(wide)+len(medium)+len(narrow))
	}

	t.lower = make(map[rune][]rune)
	for i := 0; i < len(lower); {
		if i+2 > len(lower) || i+2+int(lower[i+1]) > len(lower) {
			return nil, fmt.Errorf("section \"lower\": cut off")
		}
		count := int(lower[i+1])
		mapped := make([]rune, count)
		for j := range mapped {
			mapped[j] = rune(lower[i+2+j])
		}
		t.lower[rune(lower[i])] = mapped
		i += 2 + count
	}
	return t, t.check()
}

// check makes sure every index the lookups follow stays inside its table,
// so a damaged file fails at load and not in the middle of a message.
func (t *unicodeTables) check() error {
	const pages = 0x110000 >> 8
	if len(t.mapRanges) < 2 || len(t.mapRanges)%2 != 0 || t.mapRanges[0] != 0 {
		return fmt.Errorf("mapping ranges: bad length or start")
	}
	for i := 0; i < len(t.mapRanges); i += 2 {
		descriptor := t.mapRanges[i+1]
		if descriptor&0xff > 2 && int(descriptor>>8&0xffff)+int(descriptor>>24) > len(t.mapData) {
			return fmt.Errorf("mapping range %d points outside the data", i/2)
		}
	}
	for _, index := range [][]uint8{t.decompIndex, t.cccIndex, t.compIndex} {
		if len(index) != pages {
			return fmt.Errorf("a page index has %d entries, want %d", len(index), pages)
		}
	}
	for page := 0; page < pages; page++ {
		if (int(t.decompIndex[page])+1)*257 > len(t.decompBlock) || (int(t.compIndex[page])+1)*257 > len(t.compBlock) || (int(t.cccIndex[page])+1)*256 > len(t.cccBlock) {
			return fmt.Errorf("page %d points outside its table", page)
		}
	}
	for _, value := range t.decompBlock {
		if int(value>>2) > len(t.decompData) {
			return fmt.Errorf("a decomposition points outside the data")
		}
	}
	for _, value := range t.compBlock {
		if int(value)+1 >= len(t.compData) && int(value) != len(t.compData) {
			return fmt.Errorf("a composition points outside the data")
		}
	}
	if len(t.bidi)%3 != 0 || len(t.caseClass)%3 != 0 {
		return fmt.Errorf("a range table is cut off")
	}
	for _, list := range [][]uint32{t.combining, t.virama, t.joinR, t.joinL, t.joinD} {
		if !sort.SliceIsSorted(list, func(i, j int) bool { return list[i] < list[j] }) {
			return fmt.Errorf("a code point list is not sorted")
		}
	}
	return nil
}

// inList reports whether a sorted list holds the code point.
func inList(list []uint32, c uint32) bool {
	i := sort.Search(len(list), func(i int) bool { return list[i] >= c })
	return i < len(list) && list[i] == c
}

// rangeValue looks a code point up in sorted triples (first, last, value)
// and returns 0 where no range covers it.
func rangeValue(triples []uint32, c uint32) uint32 {
	count := len(triples) / 3
	i := sort.Search(count, func(i int) bool { return triples[i*3+1] >= c })
	if i < count && c >= triples[i*3] {
		return triples[i*3+2]
	}
	return 0
}
