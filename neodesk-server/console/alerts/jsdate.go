package alerts

import (
	"math"
	"time"
	"unicode/utf16"
)

// dateParse is JavaScript's Date.parse as Node's engine does it: the time
// a text names in milliseconds since 1970, and false where Date.parse
// answers NaN. loc is the time zone of the process, which a text without
// a zone of its own is read in.
//
// The device list carries last_online as text, and the offline sweep read
// it with Date.parse. That function takes far more than ISO dates:
// "Nov 9 2026 10:00 GMT+1", "11/9/2026", "12", words it skips, text in
// brackets it skips. The sweep's answer for a device depends on all of it,
// so this follows the engine's parser step by step: first the ISO form
// (2026-11-09T10:00:00.000Z, a date alone is UTC, a date with a time and
// no zone is local time), then, from where that stops, the older form in
// which numbers, month names, AM and PM, zone names and offsets may come
// in almost any order.
func dateParse(text string, loc *time.Location) (float64, bool) {
	p := newDateScanner(utf16.Encode([]rune(text)))
	var (
		day  dayComposer
		tm   timeComposer
		zone zoneComposer
	)
	zone.init()
	tm.hourOffset = none

	next, valid := parseISODateTime(p, &day, &tm, &zone)
	if !valid {
		return 0, false
	}
	hasReadNumber := !day.empty()
	for tok := next; tok.tag != tagEnd; tok = p.next() {
		switch {
		case tok.tag == tagNumber:
			hasReadNumber = true
			n := tok.value
			switch {
			case p.skipSymbol(':'):
				if p.skipSymbol(':') {
					// n + "::"
					if !tm.empty() {
						return 0, false
					}
					tm.add(n)
					tm.add(0)
				} else {
					// n + ":"
					if !tm.add(n) {
						return 0, false
					}
					if p.peek().isSymbol('.') {
						p.next()
					}
				}
			case p.skipSymbol('.') && tm.expecting(n):
				tm.add(n)
				if p.peek().tag != tagNumber {
					return 0, false
				}
				tm.addFinal(readMilliseconds(p.next()))
			case zone.expecting(n):
				zone.minute = n
			case tm.expecting(n):
				tm.addFinal(n)
				// After the last part of a time comes the end, white
				// space, "Z", "+" or "-".
				peek := p.peek()
				if peek.tag != tagEnd && peek.tag != tagSpace && !peek.isKeywordZ() && !peek.isSign() {
					return 0, false
				}
			default:
				if !day.add(n) {
					return 0, false
				}
				p.skipSymbol('-')
			}
		case tok.tag >= tagKeyword:
			kind, value := keywordKind(tok.tag), tok.value
			switch {
			case kind == wordAMPM && !tm.empty():
				tm.hourOffset = value
			case kind == wordMonth:
				day.namedMonth = value
				p.skipSymbol('-')
			case kind == wordZone && hasReadNumber:
				zone.set(value)
			default:
				// A word that means nothing is fine before the first
				// number, with something between it and the number.
				if hasReadNumber || p.peek().tag == tagNumber {
					return 0, false
				}
			}
		case tok.isSign() && (zone.isUTC() || !tm.empty()):
			// An offset, after UTC or after a time
			zone.sign = signOf(tok.value)
			n, length := 0, 0
			if p.peek().tag == tagNumber {
				number := p.next()
				n, length = number.value, number.length
			}
			hasReadNumber = true
			switch {
			case p.peek().isSymbol(':'):
				zone.hour = n
				zone.minute = none
			case length == 2 || length == 1:
				zone.hour = n
				zone.minute = 0
			case length == 4 || length == 3:
				zone.hour = n / 100
				zone.minute = n % 100
			default:
				return 0, false
			}
		case (tok.isSign() || tok.isSymbol(')')) && hasReadNumber:
			return 0, false
		}
	}

	year, month, date, ok := day.write()
	if !ok {
		return 0, false
	}
	hour, minute, second, ms, ok := tm.write()
	if !ok {
		return 0, false
	}
	offset, hasOffset, ok := zone.write()
	if !ok {
		return 0, false
	}

	days, ok := makeDay(year, month, date)
	if !ok {
		return 0, false
	}
	local := days*msPerDay + float64(((hour*60+minute)*60+second)*1000+ms)
	var utc float64
	if hasOffset {
		utc = local - float64(offset)*1000
	} else {
		if math.Abs(local) > maxTimeBeforeUTC {
			return 0, false
		}
		utc = localToUTC(int64(local), loc)
	}
	if math.Abs(utc) > maxTime {
		return 0, false
	}
	return utc, true
}

const (
	msPerDay = 86400000.0
	// maxTime is the largest time a Date holds, 100 million days.
	maxTime = 8.64e15
	// maxTimeBeforeUTC is how far a local time may be from 1970 to be
	// worth converting: the largest time and a month.
	maxTimeBeforeUTC = maxTime + 30*msPerDay
	// none marks a part that was not given.
	none = math.MaxInt32
)

// makeDay is the number of days from 1970 to a date. The day of the month
// counts on past the month's end (February 31 is March 3).
func makeDay(year, month, date int) (float64, bool) {
	if year < -1000000 || year > 1000000 {
		return 0, false
	}
	const yearDelta = 399999
	const baseDay = 365*(1970+yearDelta) + (1970+yearDelta)/4 - (1970+yearDelta)/100 + (1970+yearDelta)/400
	y := year + yearDelta
	days := 365*y + y/4 - y/100 + y/400 - baseDay
	if year%4 != 0 || (year%100 == 0 && year%400 != 0) {
		days += [...]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}[month]
	} else {
		days += [...]int{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335}[month]
	}
	return float64(days-1) + float64(date), true
}

// localToUTC reads a clock time of loc as an instant. A time the clocks
// skip (the hour lost in spring) is read with the offset from before the
// change, and of a time that comes twice (the hour repeated in autumn)
// the first is meant.
func localToUTC(local int64, loc *time.Location) float64 {
	offsetAt := func(utcMs int64) int64 {
		_, seconds := time.UnixMilli(utcMs).In(loc).Zone()
		return int64(seconds) * 1000
	}
	const day = 86400000
	before, after := offsetAt(local-day), offsetAt(local+day)
	if first := local - before; offsetAt(first) == before {
		return float64(first)
	}
	if second := local - after; offsetAt(second) == after {
		return float64(second)
	}
	return float64(local - before)
}

// ---- the parts of a date ----

type dayComposer struct {
	comp  [3]int
	index int
	// namedMonth is 1 to 12 once a month name was read
	namedMonth int
	iso        bool
}

func (d *dayComposer) empty() bool { return d.index == 0 }

func (d *dayComposer) add(n int) bool {
	if d.index >= len(d.comp) {
		return false
	}
	d.comp[d.index] = n
	d.index++
	return true
}

func between(x, lo, hi int) bool { return x >= lo && x <= hi }
func isMonth(x int) bool         { return between(x, 1, 12) }
func isDay(x int) bool           { return between(x, 1, 31) }
func isMinute(x int) bool        { return between(x, 0, 59) }
func isHour(x int) bool          { return between(x, 0, 23) }

// write returns the year, the month counted from 0 and the day. Parts
// that were not given are 1, and that goes for the year too: "Nov 9" is
// in 2001.
func (d *dayComposer) write() (year, month, day int, ok bool) {
	if d.index < 1 {
		return 0, 0, 0, false
	}
	for d.index < len(d.comp) {
		d.comp[d.index] = 1
		d.index++
	}
	if d.namedMonth == 0 {
		if d.iso || !isDay(d.comp[0]) {
			// year, month, day
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			// month, day, year
			month, day, year = d.comp[0], d.comp[1], d.comp[2]
		}
	} else {
		month = d.namedMonth
		if !isDay(d.comp[0]) {
			year, day = d.comp[0], d.comp[1]
		} else {
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.iso {
		switch {
		case between(year, 0, 49):
			year += 2000
		case between(year, 50, 99):
			year += 1900
		}
	}
	if !isMonth(month) || !isDay(day) {
		return 0, 0, 0, false
	}
	return year, month - 1, day, true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *timeComposer) empty() bool { return t.index == 0 }

func (t *timeComposer) expecting(n int) bool {
	return (t.index == 1 && isMinute(n)) || (t.index == 2 && isMinute(n)) || (t.index == 3 && between(n, 0, 999))
}

func (t *timeComposer) add(n int) bool {
	if t.index >= len(t.comp) {
		return false
	}
	t.comp[t.index] = n
	t.index++
	return true
}

func (t *timeComposer) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < len(t.comp) {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *timeComposer) write() (hour, minute, second, ms int, ok bool) {
	for t.index < len(t.comp) {
		t.comp[t.index] = 0
		t.index++
	}
	hour, minute, second, ms = t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != none {
		if !between(hour, 0, 12) {
			return 0, 0, 0, 0, false
		}
		hour = hour%12 + t.hourOffset
	}
	if !isHour(hour) || !isMinute(minute) || !isMinute(second) || !between(ms, 0, 999) {
		// Hour 24 is midnight at the end of the day
		if hour != 24 || minute != 0 || second != 0 || ms != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return hour, minute, second, ms, true
}

type zoneComposer struct {
	sign, hour, minute int
}

func (z *zoneComposer) init() { z.sign, z.hour, z.minute = none, none, none }

func (z *zoneComposer) set(offsetHours int) {
	z.sign = 1
	if offsetHours < 0 {
		z.sign = -1
	}
	z.hour = offsetHours * z.sign
	z.minute = 0
}

func (z *zoneComposer) expecting(n int) bool {
	return z.hour != none && z.minute == none && isMinute(n)
}

func (z *zoneComposer) isUTC() bool { return z.hour == 0 && z.minute == 0 }
func (z *zoneComposer) empty() bool { return z.hour == none }

// write returns the offset from UTC in seconds, when the text gave one.
func (z *zoneComposer) write() (offset int, has, ok bool) {
	if z.sign == none {
		return 0, false, true
	}
	if z.hour == none {
		z.hour = 0
	}
	if z.minute == none {
		z.minute = 0
	}
	// The engine adds these up in 32 bits without a sign; an hour count
	// in the millions wraps around.
	total := uint32(z.hour)*3600 + uint32(z.minute)*60
	if total > math.MaxInt32 {
		return 0, false, false
	}
	offset = int(total)
	if z.sign < 0 {
		offset = -offset
	}
	return offset, true, true
}

func signOf(symbol int) int {
	if symbol == '-' {
		return -1
	}
	return 1
}

// ---- tokens ----

const (
	tagInvalid = iota - 6
	tagUnknown
	tagSpace
	tagNumber
	tagSymbol
	tagEnd
	tagKeyword // and above: the kind of word
)

// The kinds of words.
const (
	wordOther = iota // a word that means nothing
	wordMonth
	wordZone
	wordSeparator // the T between date and time
	wordAMPM
)

func keywordKind(tag int) int { return tag - tagKeyword }

type dateToken struct {
	tag    int
	length int
	value  int
}

func (t dateToken) isSymbol(c int) bool { return t.tag == tagSymbol && t.value == c }
func (t dateToken) isSign() bool        { return t.tag == tagSymbol && (t.value == '-' || t.value == '+') }
func (t dateToken) isFixedNumber(length int) bool {
	return t.tag == tagNumber && t.length == length
}
func (t dateToken) isKeywordZ() bool {
	return t.tag == tagKeyword+wordZone && t.length == 1 && t.value == 0
}

var dateKeywords = []struct {
	prefix [3]uint16
	kind   int
	value  int
}{
	{[3]uint16{'j', 'a', 'n'}, wordMonth, 1},
	{[3]uint16{'f', 'e', 'b'}, wordMonth, 2},
	{[3]uint16{'m', 'a', 'r'}, wordMonth, 3},
	{[3]uint16{'a', 'p', 'r'}, wordMonth, 4},
	{[3]uint16{'m', 'a', 'y'}, wordMonth, 5},
	{[3]uint16{'j', 'u', 'n'}, wordMonth, 6},
	{[3]uint16{'j', 'u', 'l'}, wordMonth, 7},
	{[3]uint16{'a', 'u', 'g'}, wordMonth, 8},
	{[3]uint16{'s', 'e', 'p'}, wordMonth, 9},
	{[3]uint16{'o', 'c', 't'}, wordMonth, 10},
	{[3]uint16{'n', 'o', 'v'}, wordMonth, 11},
	{[3]uint16{'d', 'e', 'c'}, wordMonth, 12},
	{[3]uint16{'a', 'm', 0}, wordAMPM, 0},
	{[3]uint16{'p', 'm', 0}, wordAMPM, 12},
	{[3]uint16{'u', 't', 0}, wordZone, 0},
	{[3]uint16{'u', 't', 'c'}, wordZone, 0},
	{[3]uint16{'z', 0, 0}, wordZone, 0},
	{[3]uint16{'g', 'm', 't'}, wordZone, 0},
	{[3]uint16{'c', 'd', 't'}, wordZone, -5},
	{[3]uint16{'c', 's', 't'}, wordZone, -6},
	{[3]uint16{'e', 'd', 't'}, wordZone, -4},
	{[3]uint16{'e', 's', 't'}, wordZone, -5},
	{[3]uint16{'m', 'd', 't'}, wordZone, -6},
	{[3]uint16{'m', 's', 't'}, wordZone, -7},
	{[3]uint16{'p', 'd', 't'}, wordZone, -7},
	{[3]uint16{'p', 's', 't'}, wordZone, -8},
	{[3]uint16{'t', 0, 0}, wordSeparator, 0},
}

// lookupKeyword finds a word by its first three letters. Only a month
// name may be longer than its entry.
func lookupKeyword(prefix [3]uint16, length int) (kind, value int) {
	for _, entry := range dateKeywords {
		if entry.prefix == prefix && (length <= 3 || entry.kind == wordMonth) {
			return entry.kind, entry.value
		}
	}
	return wordOther, 0
}

// isDateSpace is the engine's white space without line ends.
func isDateSpace(c uint16) bool {
	switch c {
	case '\t', '\v', '\f', ' ', 0xA0, 0x1680, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func isDateSpaceOrLineEnd(c uint16) bool {
	return isDateSpace(c) || c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029
}

// dateScanner cuts the text into tokens. It works on UTF-16 units, as the
// engine does, and a NUL ends the text.
type dateScanner struct {
	units []uint16
	index int
	ch    uint16
	ahead dateToken
}

func newDateScanner(units []uint16) *dateScanner {
	p := &dateScanner{units: units}
	p.advance()
	p.ahead = p.scan()
	return p
}

func (p *dateScanner) advance() {
	p.ch = 0
	if p.index < len(p.units) {
		p.ch = p.units[p.index]
	}
	p.index++
}

func (p *dateScanner) next() dateToken {
	tok := p.ahead
	p.ahead = p.scan()
	return tok
}

func (p *dateScanner) peek() dateToken { return p.ahead }

func (p *dateScanner) skipSymbol(c int) bool {
	if p.ahead.isSymbol(c) {
		p.ahead = p.scan()
		return true
	}
	return false
}

// maxDigits is how many digits of a number count; the rest is skipped.
const maxDigits = 9

func (p *dateScanner) scan() dateToken {
	start := p.index
	switch {
	case p.ch == 0:
		return dateToken{tag: tagEnd, value: -1}
	case p.ch >= '0' && p.ch <= '9':
		n, digits := 0, 0
		for p.ch == '0' {
			p.advance()
		}
		for p.ch >= '0' && p.ch <= '9' {
			if digits < maxDigits {
				n = n*10 + int(p.ch-'0')
			}
			digits++
			p.advance()
		}
		return dateToken{tag: tagNumber, length: p.index - start, value: n}
	case p.ch == ':' || p.ch == '-' || p.ch == '+' || p.ch == '.' || p.ch == ')':
		symbol := int(p.ch)
		p.advance()
		return dateToken{tag: tagSymbol, length: 1, value: symbol}
	case p.ch >= 'A' && !isDateSpace(p.ch):
		var prefix [3]uint16
		length := 0
		for ; p.ch >= 'A' && !isDateSpace(p.ch); length++ {
			if length < len(prefix) {
				prefix[length] = p.ch | 0x20
			}
			p.advance()
		}
		kind, value := lookupKeyword(prefix, length)
		return dateToken{tag: tagKeyword + kind, length: length, value: value}
	case isDateSpaceOrLineEnd(p.ch):
		p.advance()
		return dateToken{tag: tagSpace, length: p.index - start, value: -1}
	case p.ch == '(':
		// Text in brackets is skipped, brackets inside it included
		balance := 0
		for {
			switch p.ch {
			case ')':
				balance--
			case '(':
				balance++
			}
			p.advance()
			if balance <= 0 || p.ch == 0 {
				break
			}
		}
		return dateToken{tag: tagUnknown, length: 1, value: -1}
	}
	p.advance()
	return dateToken{tag: tagUnknown, length: 1, value: -1}
}

// readMilliseconds takes the first three digits of a fraction of a
// second: "5" is 500, "05" is 50, "123456" is 123.
func readMilliseconds(tok dateToken) int {
	number, length := tok.value, tok.length
	switch {
	case length == 1:
		return number * 100
	case length == 2:
		return number * 10
	case length > 3:
		if length > maxDigits {
			length = maxDigits
		}
		factor := 1
		for ; length > 3; length-- {
			factor *= 10
		}
		return number / factor
	}
	return number
}

// parseISODateTime reads as much of the text as is an ISO date and time:
// [+-yy]yyyy[-MM[-DD]][THH:mm[:ss[.sss]][Z|+hh:mm|-hh:mm]]. It returns the
// token the older form goes on from (the end token when the whole text was
// ISO). valid is false for a text that started a time after a "T" and got
// it wrong: such a text is no date in either form.
func parseISODateTime(p *dateScanner, day *dayComposer, tm *timeComposer, zone *zoneComposer) (next dateToken, valid bool) {
	switch {
	case p.peek().isSign():
		// The sign is handed on when no six-digit year follows it
		sign := p.next()
		if !p.peek().isFixedNumber(6) {
			return sign, true
		}
		year := p.next().value
		if sign.value == '-' && year == 0 {
			return sign, true
		}
		day.add(signOf(sign.value) * year)
	case p.peek().isFixedNumber(4):
		day.add(p.next().value)
	default:
		return p.next(), true
	}
	if p.skipSymbol('-') {
		if !p.peek().isFixedNumber(2) || !isMonth(p.peek().value) {
			return p.next(), true
		}
		day.add(p.next().value)
		if p.skipSymbol('-') {
			if !p.peek().isFixedNumber(2) || !isDay(p.peek().value) {
				return p.next(), true
			}
			day.add(p.next().value)
		}
	}
	if p.peek().tag != tagKeyword+wordSeparator {
		if p.peek().tag != tagEnd {
			return p.next(), true
		}
	} else {
		invalid := dateToken{tag: tagInvalid}
		p.next()
		if !p.peek().isFixedNumber(2) || !between(p.peek().value, 0, 24) {
			return invalid, false
		}
		// 24:00 is allowed, and nothing else that starts with 24
		hour24 := p.peek().value == 24
		tm.add(p.next().value)
		if !p.skipSymbol(':') {
			return invalid, false
		}
		if !p.peek().isFixedNumber(2) || !isMinute(p.peek().value) || (hour24 && p.peek().value > 0) {
			return invalid, false
		}
		tm.add(p.next().value)
		if p.skipSymbol(':') {
			if !p.peek().isFixedNumber(2) || !isMinute(p.peek().value) || (hour24 && p.peek().value > 0) {
				return invalid, false
			}
			tm.add(p.next().value)
			if p.skipSymbol('.') {
				if p.peek().tag != tagNumber || (hour24 && p.peek().value > 0) {
					return invalid, false
				}
				tm.add(readMilliseconds(p.next()))
			}
		}
		switch {
		case p.peek().isKeywordZ():
			p.next()
			zone.set(0)
		case p.peek().isSign():
			zone.sign = signOf(p.next().value)
			if p.peek().isFixedNumber(4) {
				// hhmm
				hourMinute := p.next().value
				hour, minute := hourMinute/100, hourMinute%100
				if !isHour(hour) || !isMinute(minute) {
					return invalid, false
				}
				zone.hour, zone.minute = hour, minute
			} else {
				// hh:mm
				if !p.peek().isFixedNumber(2) || !isHour(p.peek().value) {
					return invalid, false
				}
				zone.hour = p.next().value
				if !p.skipSymbol(':') {
					return invalid, false
				}
				if !p.peek().isFixedNumber(2) || !isMinute(p.peek().value) {
					return invalid, false
				}
				zone.minute = p.next().value
			}
		}
		if p.peek().tag != tagEnd {
			return invalid, false
		}
	}
	// A date alone is UTC; a date with a time and no zone is local time.
	if zone.empty() && tm.empty() {
		zone.set(0)
	}
	day.iso = true
	return dateToken{tag: tagEnd, value: -1}, true
}
