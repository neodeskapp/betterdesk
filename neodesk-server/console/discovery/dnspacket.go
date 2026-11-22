package discovery

import (
	"encoding/binary"
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
)

// The DNS messages of the mDNS announcement, written and read the way the
// npm package dns-packet 5.6 (under multicast-dns 7.2 and bonjour-service
// 1.4) did: no name compression when writing, and a reader that accepts
// and refuses exactly what that one did, so the same datagrams get an
// answer.

// dnsRecord is a resource record the announcement sends.
type dnsRecord struct {
	Name string
	// Type is "PTR", "SRV", "TXT", "A" or "AAAA".
	Type string
	TTL  uint32
	// Data is the target name of a PTR, the address of an A or AAAA
	// record, srvData or the strings of a TXT record ([][]byte).
	Data any
}

// srvData is the data of an SRV record.
type srvData struct {
	Port   uint16
	Target string
}

// dataEqual is fast-deep-equal on two records' data.
func dataEqual(a, b any) bool {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case srvData:
		y, ok := b.(srvData)
		return ok && x == y
	case [][]byte:
		y, ok := b.([][]byte)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if string(x[i]) != string(y[i]) {
				return false
			}
		}
		return true
	}
	return false
}

var typeCodes = map[string]uint16{
	"A": 1, "NULL": 10, "AAAA": 28, "AFSDB": 18, "APL": 42, "CAA": 257, "CDNSKEY": 60, "CDS": 59, "CERT": 37,
	"CNAME": 5, "DHCID": 49, "DLV": 32769, "DNAME": 39, "DNSKEY": 48, "DS": 43, "HIP": 55, "HINFO": 13,
	"IPSECKEY": 45, "KEY": 25, "KX": 36, "LOC": 29, "MX": 15, "NAPTR": 35, "NS": 2, "NSEC": 47, "NSEC3": 50,
	"NSEC3PARAM": 51, "PTR": 12, "RRSIG": 46, "RP": 17, "SIG": 24, "SOA": 6, "SPF": 99, "SRV": 33, "SSHFP": 44,
	"TA": 32768, "TKEY": 249, "TLSA": 52, "TSIG": 250, "TXT": 16, "AXFR": 252, "IXFR": 251, "OPT": 41, "ANY": 255,
}

var typeNames = func() map[uint16]string {
	m := make(map[uint16]string, len(typeCodes))
	for name, code := range typeCodes {
		m[code] = name
	}
	return m
}()

// typeName is dns-packet's name of a record type.
func typeName(code uint16) string {
	if name, ok := typeNames[code]; ok {
		return name
	}
	return "UNKNOWN_" + strconv.Itoa(int(code))
}

const (
	responseFlag      = 1 << 15
	authoritativeFlag = 1 << 10
	classIN           = 1
)

// isLineTerminator is what a JavaScript regular expression with the m
// flag takes as the end of a line.
func isLineTerminator(r rune) bool { return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 }

// stripDots removes a dot at the start or end of the name, or next to a
// line terminator (dns-packet's str.replace(/^\.|\.$/gm, ”)).
func stripDots(name string) string {
	runes := []rune(name)
	var out strings.Builder
	for i, r := range runes {
		if r == '.' && (i == 0 || isLineTerminator(runes[i-1]) || i == len(runes)-1 || isLineTerminator(runes[i+1])) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// appendName writes a name as labels, without compression. A label's
// length byte keeps the low 8 bits of its length, as a JavaScript byte
// array does.
func appendName(dst []byte, name string) []byte {
	n := stripDots(name)
	if n != "" {
		for _, label := range strings.Split(n, ".") {
			dst = append(dst, byte(len(label)))
			dst = append(dst, label...)
		}
	}
	return append(dst, 0)
}

// appendRecord writes one record of class IN.
func appendRecord(dst []byte, r dnsRecord) []byte {
	dst = appendName(dst, r.Name)
	dst = binary.BigEndian.AppendUint16(dst, typeCodes[r.Type])
	dst = binary.BigEndian.AppendUint16(dst, classIN)
	dst = binary.BigEndian.AppendUint32(dst, r.TTL)
	switch r.Type {
	case "PTR":
		target := appendName(nil, r.Data.(string))
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(target)))
		dst = append(dst, target...)
	case "SRV":
		srv := r.Data.(srvData)
		target := appendName(nil, srv.Target)
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(target)+6))
		dst = binary.BigEndian.AppendUint16(dst, 0)
		dst = binary.BigEndian.AppendUint16(dst, 0)
		dst = binary.BigEndian.AppendUint16(dst, srv.Port)
		dst = append(dst, target...)
	case "TXT":
		var data []byte
		for _, s := range r.Data.([][]byte) {
			data = append(data, byte(len(s)))
			data = append(data, s...)
		}
		dst = binary.BigEndian.AppendUint16(dst, uint16(len(data)))
		dst = append(dst, data...)
	case "A":
		dst = binary.BigEndian.AppendUint16(dst, 4)
		dst = append(dst, ipBytes(r.Data.(string), 4)...)
	case "AAAA":
		dst = binary.BigEndian.AppendUint16(dst, 16)
		dst = append(dst, ipBytes(r.Data.(string), 16)...)
	}
	return dst
}

// ipBytes is an address as bytes; zeros when it does not parse.
func ipBytes(text string, size int) []byte {
	ip := net.ParseIP(text)
	if size == 4 {
		if v4 := ip.To4(); v4 != nil {
			return v4
		}
		return make([]byte, 4)
	}
	if v6 := ip.To16(); v6 != nil {
		return v6
	}
	return make([]byte, 16)
}

// encodeResponse is multicast-dns's respond: id 0, the response and
// authoritative answer flags, the answers and additional records.
func encodeResponse(answers, additionals []dnsRecord) []byte {
	out := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(out[2:], responseFlag|authoritativeFlag)
	binary.BigEndian.PutUint16(out[6:], uint16(len(answers)))
	binary.BigEndian.PutUint16(out[10:], uint16(len(additionals)))
	for _, r := range answers {
		out = appendRecord(out, r)
	}
	for _, r := range additionals {
		out = appendRecord(out, r)
	}
	return out
}

// encodeQuery is multicast-dns's query for one name of type ANY.
func encodeQuery(name string) []byte {
	out := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(out[4:], 1)
	out = appendName(out, name)
	out = binary.BigEndian.AppendUint16(out, typeCodes["ANY"])
	return binary.BigEndian.AppendUint16(out, classIN)
}

// dnsQuestion is a question as dns-packet decodes it.
type dnsQuestion struct {
	Name string
	Type string
}

// dnsMessage is what the announcement reads from a datagram.
type dnsMessage struct {
	Response    bool
	Questions   []dnsQuestion
	Answers     []string
	Additionals []string
}

// errDecode is any datagram dns-packet's decode throws on.
var errDecode = errors.New("cannot decode the DNS message")

// jsDecoder reads a datagram with the offsets dns-packet computes. An
// offset is a JavaScript number: reading past the end of a string yields
// NaN, and a decoder that gets NaN (or 0) as its offset starts at 0.
type jsDecoder struct {
	b []byte
}

func start(off float64) float64 {
	if math.IsNaN(off) {
		return 0
	}
	return off
}

func (d *jsDecoder) inRange(off float64, n int) bool {
	return !math.IsNaN(off) && off >= 0 && off+float64(n) <= float64(len(d.b))
}

func (d *jsDecoder) u8(off float64) (int, error) {
	if !d.inRange(off, 1) {
		return 0, errDecode
	}
	return int(d.b[int(off)]), nil
}

func (d *jsDecoder) u16(off float64) (int, error) {
	if !d.inRange(off, 2) {
		return 0, errDecode
	}
	return int(binary.BigEndian.Uint16(d.b[int(off):])), nil
}

func (d *jsDecoder) u32(off float64) error {
	if !d.inRange(off, 4) {
		return errDecode
	}
	return nil
}

// at is buf[off]: the byte, or ok false for undefined.
func (d *jsDecoder) at(off float64) (int, bool) {
	if !d.inRange(off, 1) {
		return 0, false
	}
	return int(d.b[int(off)]), true
}

// sliceLen is the length of buf.slice(start, end).
func (d *jsDecoder) sliceLen(from, to float64) float64 {
	n := float64(len(d.b))
	clamp := func(v float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		v = math.Trunc(v)
		if v < 0 {
			v += n
			if v < 0 {
				v = 0
			}
		}
		if v > n {
			v = n
		}
		return v
	}
	a, b := clamp(from), clamp(to)
	if b < a {
		return 0
	}
	return b - a
}

// name decodes a name and returns it with the bytes it took.
func (d *jsDecoder) name(off float64, mail bool) (string, float64, error) {
	off = start(off)
	var labels []string
	oldOffset := off
	total, consumed := 0, 0.0
	jumped := false
	for {
		if off >= float64(len(d.b)) {
			return "", 0, errDecode
		}
		l := int(d.b[int(off)])
		off++
		if !jumped {
			consumed++
		}
		switch {
		case l == 0:
			if len(labels) == 0 {
				return ".", consumed, nil
			}
			return strings.Join(labels, "."), consumed, nil
		case l&0xc0 == 0:
			if off+float64(l) > float64(len(d.b)) {
				return "", 0, errDecode
			}
			total += l + 1
			if total > 254 {
				return "", 0, errDecode
			}
			label := decodeUTF8(d.b[int(off) : int(off)+l])
			if mail {
				label = strings.ReplaceAll(label, ".", `\.`)
			}
			labels = append(labels, label)
			off += float64(l)
			if !jumped {
				consumed += float64(l)
			}
		case l&0xc0 == 0xc0:
			if off+1 > float64(len(d.b)) {
				return "", 0, errDecode
			}
			jump := float64(int(binary.BigEndian.Uint16(d.b[int(off)-1:])) - 0xc000)
			if jump >= oldOffset {
				return "", 0, errDecode
			}
			off, oldOffset = jump, jump
			if !jumped {
				consumed++
			}
			jumped = true
		default:
			return "", 0, errDecode
		}
	}
}

// str is dns-packet's string.decode: the bytes it took, NaN when its
// length byte is past the end.
func (d *jsDecoder) str(off float64) float64 {
	off = start(off)
	l, ok := d.at(off)
	if !ok {
		return math.NaN()
	}
	return float64(l + 1)
}

// typeBitmap is typebitmap.decode.
func (d *jsDecoder) typeBitmap(off, length float64) (float64, error) {
	off = start(off)
	old := off
	for off-old < length {
		if _, err := d.u8(off); err != nil {
			return 0, err
		}
		off++
		windowLength, err := d.u8(off)
		if err != nil {
			return 0, err
		}
		off++
		for i := 0; i < windowLength; i++ {
			if _, err := d.u8(off + float64(i)); err != nil {
				return 0, err
			}
		}
		off += float64(windowLength)
	}
	return off - old, nil
}

// rdata decodes the data of a record of the type at off, where its
// length field is, and returns the bytes it took.
func (d *jsDecoder) rdata(typ string, off float64) (float64, error) {
	off = start(off)
	o := off
	switch typ {
	case "A":
		return 6, nil
	case "AAAA":
		return 18, nil
	case "PTR", "CNAME", "DNAME":
		_, n, err := d.name(o+2, false)
		return n + 2, err
	case "TXT":
		remaining, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		left := float64(remaining)
		off += 2
		for left > 0 {
			l, ok := d.at(off)
			off++
			left--
			if !ok {
				return math.NaN(), nil
			}
			if left < float64(l) {
				return 0, errDecode
			}
			off += float64(l)
			left -= float64(l)
		}
		return off - o, nil
	case "NULL":
		l, err := d.u16(o)
		return float64(l) + 2, err
	case "HINFO":
		off += 2
		off += d.str(off)
		off += d.str(off)
		return off - o, nil
	case "SRV":
		l, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		for _, at := range []float64{o + 2, o + 4, o + 6} {
			if _, err := d.u16(at); err != nil {
				return 0, err
			}
		}
		if _, _, err := d.name(o+8, false); err != nil {
			return 0, err
		}
		return float64(l) + 2, nil
	case "CAA":
		l, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		if _, err := d.u8(o + 2); err != nil {
			return 0, err
		}
		return float64(l) + 2, nil
	case "NS":
		l, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		if _, _, err := d.name(o+2, false); err != nil {
			return 0, err
		}
		return float64(l) + 2, nil
	case "SOA":
		off += 2
		_, n, err := d.name(off, false)
		if err != nil {
			return 0, err
		}
		off += n
		_, n, err = d.name(off, true)
		if err != nil {
			return 0, err
		}
		off += n
		for i := 0; i < 5; i++ {
			if err := d.u32(off); err != nil {
				return 0, err
			}
			off += 4
		}
		return off - o, nil
	case "MX":
		off += 2
		if _, err := d.u16(off); err != nil {
			return 0, err
		}
		off += 2
		_, n, err := d.name(off, false)
		if err != nil {
			return 0, err
		}
		return off + n - o, nil
	case "DNSKEY":
		length, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		off += 2
		if _, err := d.u16(off); err != nil {
			return 0, err
		}
		off += 2
		protocol, err := d.u8(off)
		if err != nil {
			return 0, err
		}
		if protocol != 3 {
			return 0, errDecode
		}
		off++
		if _, err := d.u8(off); err != nil {
			return 0, err
		}
		off++
		off += d.sliceLen(off, o+float64(length)+2)
		return off - o, nil
	case "RRSIG":
		length, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		off += 2
		for _, n := range []int{2, 1, 1, 4, 4, 4, 2} {
			var err error
			switch n {
			case 1:
				_, err = d.u8(off)
			case 2:
				_, err = d.u16(off)
			default:
				err = d.u32(off)
			}
			if err != nil {
				return 0, err
			}
			off += float64(n)
		}
		_, n, err := d.name(off, false)
		if err != nil {
			return 0, err
		}
		off += n
		off += d.sliceLen(off, o+float64(length)+2)
		return off - o, nil
	case "RP":
		off += 2
		_, n, err := d.name(off, true)
		if err != nil {
			return 0, err
		}
		off += n
		_, n, err = d.name(off, false)
		if err != nil {
			return 0, err
		}
		return off + n - o, nil
	case "NSEC":
		length, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		off += 2
		_, n, err := d.name(off, false)
		if err != nil {
			return 0, err
		}
		off += n
		n, err = d.typeBitmap(off, float64(length)-(off-o))
		if err != nil {
			return 0, err
		}
		return off + n - o, nil
	case "NSEC3":
		length, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		off += 2
		for _, n := range []int{1, 1, 2} {
			var err error
			if n == 1 {
				_, err = d.u8(off)
			} else {
				_, err = d.u16(off)
			}
			if err != nil {
				return 0, err
			}
			off += float64(n)
		}
		salt, err := d.u8(off)
		if err != nil {
			return 0, err
		}
		off += 1 + float64(salt)
		hash, err := d.u8(off)
		if err != nil {
			return 0, err
		}
		off += 1 + float64(hash)
		n, err := d.typeBitmap(off, float64(length)-(off-o))
		if err != nil {
			return 0, err
		}
		return off + n - o, nil
	case "SSHFP":
		off += 2
		off++
		hash, _ := d.at(off)
		off++
		switch hash {
		case 1:
			off += 20
		case 2:
			off += 32
		default:
			return math.NaN(), nil
		}
		return off - o, nil
	case "DS":
		length, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		off += 2
		if _, err := d.u16(off); err != nil {
			return 0, err
		}
		off += 2
		for i := 0; i < 2; i++ {
			if _, err := d.u8(off); err != nil {
				return 0, err
			}
			off++
		}
		off += d.sliceLen(off, o+float64(length)+2)
		return off - o, nil
	case "NAPTR":
		off += 2
		for i := 0; i < 2; i++ {
			if _, err := d.u16(off); err != nil {
				return 0, err
			}
			off += 2
		}
		for i := 0; i < 3; i++ {
			off += d.str(off)
		}
		_, n, err := d.name(off, false)
		if err != nil {
			return 0, err
		}
		return off + n - o, nil
	case "TLSA":
		length, err := d.u16(o)
		if err != nil {
			return 0, err
		}
		off += 2
		for i := 0; i < 3; i++ {
			if _, err := d.u8(off); err != nil {
				return 0, err
			}
			off++
		}
		off += d.sliceLen(off, o+float64(length)+2)
		return off - o, nil
	}
	l, err := d.u16(o)
	return float64(l) + 2, err
}

// options is ropt.decode, the options of an OPT record.
func (d *jsDecoder) options(off float64) (float64, error) {
	off = start(off)
	o := off
	rdlen, err := d.u16(off)
	if err != nil {
		return 0, err
	}
	left := float64(rdlen)
	off += 2
	for left > 0 {
		at := off
		code, err := d.u16(at)
		if err != nil {
			return 0, err
		}
		l, err := d.u16(at + 2)
		if err != nil {
			return 0, err
		}
		switch code {
		case 8:
			if _, err := d.u16(at + 4); err != nil {
				return 0, err
			}
			for _, p := range []float64{at + 6, at + 7} {
				if _, err := d.u8(p); err != nil {
					return 0, err
				}
			}
		case 11:
			if l > 0 {
				if _, err := d.u16(at + 4); err != nil {
					return 0, err
				}
			}
		case 14:
			for i := 0; i < l; i += 2 {
				if _, err := d.u16(at + 4 + float64(i)); err != nil {
					return 0, err
				}
			}
		}
		off += float64(l) + 4
		left -= float64(l) + 4
	}
	return off - o, nil
}

// record decodes one record and returns its name and the bytes it took.
func (d *jsDecoder) record(off float64) (string, float64, error) {
	off = start(off)
	old := off
	name, n, err := d.name(off, false)
	if err != nil {
		return "", 0, err
	}
	off += n
	code, err := d.u16(off)
	if err != nil {
		return "", 0, err
	}
	typ := typeName(uint16(code))
	if typ == "OPT" {
		for _, p := range []struct {
			at   float64
			size int
		}{{off + 2, 2}, {off + 4, 1}, {off + 5, 1}, {off + 6, 2}} {
			if !d.inRange(p.at, p.size) {
				return "", 0, errDecode
			}
		}
		n, err := d.options(off + 8)
		if err != nil {
			return "", 0, err
		}
		return name, off + 8 + n - old, nil
	}
	if _, err := d.u16(off + 2); err != nil {
		return "", 0, err
	}
	if err := d.u32(off + 4); err != nil {
		return "", 0, err
	}
	n, err = d.rdata(typ, off+8)
	if err != nil {
		return "", 0, err
	}
	return name, off + 8 + n - old, nil
}

// decodeMessage is dns-packet's decode, kept to what the announcement
// reads: whether it is a response, the questions, and the names of the
// answers and additional records. It fails where dns-packet threw.
func decodeMessage(b []byte) (*dnsMessage, error) {
	if len(b) < 12 {
		return nil, errDecode
	}
	d := &jsDecoder{b: b}
	m := &dnsMessage{Response: binary.BigEndian.Uint16(b[2:])&responseFlag != 0}
	counts := [4]int{}
	for i := range counts {
		counts[i] = int(binary.BigEndian.Uint16(b[4+2*i:]))
	}
	off := 12.0
	for i := 0; i < counts[0]; i++ {
		name, n, err := d.name(off, false)
		if err != nil {
			return nil, err
		}
		off = start(off) + n
		code, err := d.u16(off)
		if err != nil {
			return nil, err
		}
		if _, err := d.u16(off + 2); err != nil {
			return nil, err
		}
		off += 4
		m.Questions = append(m.Questions, dnsQuestion{Name: name, Type: typeName(uint16(code))})
	}
	for section := 1; section < 4; section++ {
		for i := 0; i < counts[section]; i++ {
			name, n, err := d.record(off)
			if err != nil {
				return nil, err
			}
			off += n
			switch section {
			case 1:
				m.Answers = append(m.Answers, name)
			case 3:
				m.Additionals = append(m.Additionals, name)
			}
		}
	}
	return m, nil
}
