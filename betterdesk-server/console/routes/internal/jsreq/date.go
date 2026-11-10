package jsreq

import (
	"math"
	"time"
)

// ParseDate is Date.parse(text) for the forms the API server and the
// databases write, in milliseconds since 1970:
//
//	2026-11-10T07:32:11Z                  a time with a zone: Z, +01:00, -0130
//	2026-11-10T07:32:11.123456789+01:00   of a fraction the first three digits count
//	2026-11-10T07:32                      without a zone: a time in local
//	2026-11-10 07:32:11                   the same with a space, as databases write it
//	2026-11-10, 2026-11, 2026             the start of that day, month or year in UTC
//	2026-11-10Z                           the same
//
// T and Z may be written in lower case. A day up to 31 is taken in every
// month and runs over into the next one (February 30 is March 2), and
// 24:00:00 is the end of the day. ok is false where JavaScript gives NaN,
// and for the looser forms Date.parse also takes ("Nov 10 2026 07:32
// UTC", a fraction or a short offset behind a time written with a space),
// which nothing here writes.
func ParseDate(text string, local *time.Location) (ms int64, ok bool) {
	p := dateScanner{s: text}
	year, okYear := p.number(4)
	if !okYear {
		return 0, false
	}
	month, day := 1, 1
	if !p.dateEnds() {
		var okMonth bool
		if !p.char('-') {
			return 0, false
		}
		if month, okMonth = p.number(2); !okMonth || month < 1 || month > 12 {
			return 0, false
		}
		if !p.dateEnds() {
			var okDay bool
			if !p.char('-') {
				return 0, false
			}
			if day, okDay = p.number(2); !okDay || day < 1 || day > 31 {
				return 0, false
			}
		}
	}
	if p.dateEnds() {
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).UnixMilli(), true
	}
	if !p.char('T') && !p.char('t') && !p.char(' ') {
		return 0, false
	}
	c, okClock := p.clock()
	if !okClock {
		return 0, false
	}
	nanos := c.milli * int(time.Millisecond)
	if p.pos == len(p.s) {
		return time.Date(year, time.Month(month), day, c.hour, c.minute, c.second, nanos, local).UnixMilli(), true
	}
	offset, okZone := p.zone()
	if !okZone || p.pos != len(p.s) {
		return 0, false
	}
	t := time.Date(year, time.Month(month), day, c.hour, c.minute, c.second, nanos, time.UTC)
	return t.UnixMilli() - int64(offset)*60000, true
}

// DateOf is new Date(v).getTime() for a JSON value: text through
// ParseDate, a number as milliseconds, true as 1, false and null as 0. ok
// is false where JavaScript gives NaN.
func DateOf(v any, local *time.Location) (ms int64, ok bool) {
	switch t := v.(type) {
	case nil:
		return 0, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(t) || math.Abs(t) > 8.64e15 {
			return 0, false
		}
		return int64(t), true
	case string:
		return ParseDate(t, local)
	case []any:
		// A list reads as its elements joined by commas
		return ParseDate(String(t), local)
	}
	return 0, false
}

type dateScanner struct {
	s   string
	pos int
}

func (p *dateScanner) char(c byte) bool {
	if p.pos < len(p.s) && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

// digits returns the run of digits at the position.
func (p *dateScanner) digits() string {
	start := p.pos
	for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
		p.pos++
	}
	return p.s[start:p.pos]
}

// number reads a run of exactly n digits.
func (p *dateScanner) number(n int) (int, bool) {
	run := p.digits()
	if len(run) != n {
		return 0, false
	}
	value := 0
	for i := 0; i < len(run); i++ {
		value = value*10 + int(run[i]-'0')
	}
	return value, true
}

// dateEnds reports whether the date part is all there is: the end of the
// text, or a lone Z.
func (p *dateScanner) dateEnds() bool {
	rest := p.s[p.pos:]
	return rest == "" || rest == "Z" || rest == "z"
}

type clockTime struct{ hour, minute, second, milli int }

// clock reads the time of day: hh:mm, hh:mm:ss or hh:mm:ss.fff.
func (p *dateScanner) clock() (clockTime, bool) {
	var c clockTime
	var ok bool
	if c.hour, ok = p.number(2); !ok || !p.char(':') {
		return c, false
	}
	if c.minute, ok = p.number(2); !ok {
		return c, false
	}
	if p.char(':') {
		if c.second, ok = p.number(2); !ok {
			return c, false
		}
		if p.char('.') {
			if c.milli, ok = p.fraction(); !ok {
				return c, false
			}
		}
	}
	if c.minute > 59 || c.second > 59 || c.hour > 24 || (c.hour == 24 && (c.minute != 0 || c.second != 0 || c.milli != 0)) {
		return c, false
	}
	return c, true
}

// fraction reads the digits after the point as milliseconds: the first
// three count, the rest is dropped.
func (p *dateScanner) fraction() (int, bool) {
	run := p.digits()
	if run == "" {
		return 0, false
	}
	milli := 0
	for i := 0; i < 3; i++ {
		milli *= 10
		if i < len(run) {
			milli += int(run[i] - '0')
		}
	}
	return milli, true
}

// zone reads Z or an offset (+01:00, -0130) and returns the offset in
// minutes east of UTC.
func (p *dateScanner) zone() (int, bool) {
	if p.char('Z') || p.char('z') {
		return 0, true
	}
	sign := 1
	switch {
	case p.char('+'):
	case p.char('-'):
		sign = -1
	default:
		return 0, false
	}
	run := p.digits()
	var hours, minutes int
	switch len(run) {
	case 4:
		hours = int(run[0]-'0')*10 + int(run[1]-'0')
		minutes = int(run[2]-'0')*10 + int(run[3]-'0')
	case 2:
		hours = int(run[0]-'0')*10 + int(run[1]-'0')
		if !p.char(':') {
			return 0, false
		}
		var ok bool
		if minutes, ok = p.number(2); !ok {
			return 0, false
		}
	default:
		return 0, false
	}
	if hours > 23 || minutes > 59 {
		return 0, false
	}
	return sign * (hours*60 + minutes), true
}
