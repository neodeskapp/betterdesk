package registrations

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// What the notification view model did with values in JavaScript, for
// the values package goapi reads JSON into: nil, bool, float64, string,
// []any and *goapi.Object.

// jsString is String(v). An object is "[object Object]".
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsbind.NumberString(t)
	case []any:
		return joinList(t, ",")
	}
	return "[object Object]"
}

// joinList is list.join(sep): null elements are empty.
func joinList(list []any, sep string) string {
	parts := make([]string, len(list))
	for i, item := range list {
		if item != nil {
			parts[i] = jsString(item)
		}
	}
	return strings.Join(parts, sep)
}

// orValue is a || b.
func orValue(a, b any) any {
	if goapi.Truthy(a) {
		return a
	}
	return b
}

// maxTime is the largest distance from 1970 a JavaScript date can have,
// in milliseconds.
const maxTime = 8.64e15

// dateValue is new Date(v).getTime() for a value that is not a Date: a
// number as milliseconds, text through Date.parse, true as 1, false and
// null as 0, a list as its text. ok is false where JavaScript has an
// invalid date.
func dateValue(v any, loc *time.Location) (ms float64, ok bool) {
	switch t := v.(type) {
	case nil:
		return 0, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(t) || math.Abs(t) > maxTime {
			return 0, false
		}
		return math.Trunc(t) + 0, true
	case string:
		return parseDate(t, loc)
	case []any:
		return parseDate(joinList(t, ","), loc)
	}
	return 0, false
}

// isoString is Date.prototype.toISOString for a valid time in
// milliseconds: six digits and a sign for a year outside 0 to 9999.
func isoString(ms float64) string {
	t := time.UnixMilli(int64(ms)).UTC()
	year := t.Year()
	var b strings.Builder
	switch {
	case year < 0:
		b.WriteString("-" + pad(-year, 6))
	case year > 9999:
		b.WriteString("+" + pad(year, 6))
	default:
		b.WriteString(pad(year, 4))
	}
	b.WriteString(t.Format("-01-02T15:04:05.000Z"))
	return b.String()
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// parseDate is Date.parse(text) for the forms the API server and the
// databases write, in milliseconds since 1970:
//
//	2026-11-10T07:32:11Z                  a time with a zone: Z, +01:00, -0130
//	2026-11-10T07:32:11.123456789+01:00   of a fraction the first three digits count
//	2026-11-10T07:32                      without a zone: a time in loc
//	2026-11-10 07:32:11                   the same with a space, as SQLite writes it
//	2026-11-10, 2026-11, 2026             the start of that day, month or year in UTC
//	+002026-11-10T07:32:11.000Z           a year of six digits with a sign
//
// T and Z may be written in lower case. A day up to 31 is taken in every
// month and runs over into the next one, and 24:00:00 is the end of the
// day. ok is false where JavaScript gives NaN, and for the looser forms
// Date.parse also takes ("Nov 10 2026 07:32 UTC"), which neither the API
// server nor the databases write.
func parseDate(text string, loc *time.Location) (ms float64, ok bool) {
	p := scanner{s: text}
	year, okYear := p.year()
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
		return clip(time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC))
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
		return clip(time.Date(year, time.Month(month), day, c.hour, c.minute, c.second, nanos, loc))
	}
	offset, okZone := p.zone()
	if !okZone || p.pos != len(p.s) {
		return 0, false
	}
	t := time.Date(year, time.Month(month), day, c.hour, c.minute, c.second, nanos, time.UTC)
	return clip(t.Add(-time.Duration(offset) * time.Minute))
}

// clip is TimeClip: a time further than maxTime from 1970 is invalid.
func clip(t time.Time) (float64, bool) {
	ms := float64(t.UnixMilli())
	if math.Abs(ms) > maxTime {
		return 0, false
	}
	return ms, true
}

type scanner struct {
	s   string
	pos int
}

func (p *scanner) char(c byte) bool {
	if p.pos < len(p.s) && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

// year reads four digits, or a sign and six digits (where -000000 is no
// year).
func (p *scanner) year() (int, bool) {
	sign := 0
	switch {
	case p.char('+'):
		sign = 1
	case p.char('-'):
		sign = -1
	}
	if sign == 0 {
		return p.number(4)
	}
	y, ok := p.number(6)
	if !ok || (sign < 0 && y == 0) {
		return 0, false
	}
	return sign * y, true
}

func (p *scanner) digits() string {
	start := p.pos
	for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
		p.pos++
	}
	return p.s[start:p.pos]
}

// number reads a run of exactly n digits.
func (p *scanner) number(n int) (int, bool) {
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
func (p *scanner) dateEnds() bool {
	rest := p.s[p.pos:]
	return rest == "" || rest == "Z" || rest == "z"
}

type clockTime struct{ hour, minute, second, milli int }

// clock reads the time of day: hh:mm, hh:mm:ss or hh:mm:ss.fff.
func (p *scanner) clock() (clockTime, bool) {
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
func (p *scanner) fraction() (int, bool) {
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
func (p *scanner) zone() (int, bool) {
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
