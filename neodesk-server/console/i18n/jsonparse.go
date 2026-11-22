package i18n

import (
	"math"
	"strconv"
)

// parseError is a JSON syntax error with the message V8 gives for it. The
// console shows that message for a broken language file.
type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

// maxJSONDepth is how deep arrays and objects may be nested in a text given
// to parseJSON. Deeper nesting fails with the RangeError message V8 gives
// when it runs out of stack.
const maxJSONDepth = 5000

// parseJSON is JSON.parse: strict JSON, a repeated key keeps its first
// place and its last value, numbers become float64.
func parseJSON(text string) (any, error) {
	p := &jsonParser{src: units(text)}
	v := p.parseValue()
	if p.err == nil {
		p.skipSpace()
		if p.pos < len(p.src) {
			p.failAt("Unexpected non-whitespace character after JSON")
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return v, nil
}

type jsonParser struct {
	src   []uint16
	pos   int
	depth int
	err   *parseError
}

// enter counts one more level of nesting and fails when it is too deep.
func (p *jsonParser) enter() bool {
	p.depth++
	if p.depth > maxJSONDepth {
		if p.err == nil {
			p.err = &parseError{"Maximum call stack size exceeded"}
		}
		return false
	}
	return true
}

func (p *jsonParser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// failAt reports a named problem at the current position.
func (p *jsonParser) failAt(what string) {
	if p.err != nil {
		return
	}
	line, lineStart := 1, 0
	for i := 0; i < p.pos; i++ {
		if p.src[i] == '\r' && i < p.pos-1 && p.src[i+1] == '\n' {
			i++
		}
		if p.src[i] == '\r' || p.src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	p.err = &parseError{what + " at position " + strconv.Itoa(p.pos) +
		" (line " + strconv.Itoa(line) + " column " + strconv.Itoa(1+p.pos-lineStart) + ")"}
}

// failToken reports the character at the current position as unexpected.
func (p *jsonParser) failToken() {
	if p.err != nil {
		return
	}
	if p.pos >= len(p.src) {
		p.err = &parseError{"Unexpected end of JSON input"}
		return
	}
	c := p.src[p.pos]
	switch {
	case c == '-' || (c >= '0' && c <= '9'):
		p.failAt("Unexpected number in JSON")
		return
	case c == '"':
		p.failAt("Unexpected string in JSON")
		return
	}
	whole := fromUnits(p.src)
	switch whole {
	case "NaN", "Infinity", "undefined", "[object Object]":
		p.err = &parseError{`"` + whole + `" is not valid JSON`}
		return
	}
	const context = 10
	token := fromUnits(p.src[p.pos : p.pos+1])
	n := len(p.src)
	var shown string
	switch {
	case n < 2*context+1:
		shown = `"` + whole + `"`
	case p.pos < context:
		shown = `"` + fromUnits(p.src[:p.pos+context]) + `"...`
	case p.pos < n-context:
		shown = `..."` + fromUnits(p.src[p.pos-context:p.pos+context]) + `"...`
	default:
		shown = `..."` + fromUnits(p.src[p.pos-context:]) + `"`
	}
	p.err = &parseError{"Unexpected token '" + token + "', " + shown + " is not valid JSON"}
}

func (p *jsonParser) parseValue() any {
	p.skipSpace()
	if p.pos >= len(p.src) {
		p.failToken()
		return nil
	}
	switch c := p.src[p.pos]; {
	case c == '"':
		return p.parseString()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	case c == '{':
		return p.parseObject()
	case c == '[':
		return p.parseArray()
	case c == 't':
		if p.literal("true") {
			return true
		}
	case c == 'f':
		if p.literal("false") {
			return false
		}
	case c == 'n':
		p.literal("null")
	default:
		p.failToken()
	}
	return nil
}

func (p *jsonParser) literal(word string) bool {
	for i := 0; i < len(word); i++ {
		if p.pos >= len(p.src) || p.src[p.pos] != uint16(word[i]) {
			p.failToken()
			return false
		}
		p.pos++
	}
	return true
}

func isDigit(c uint16) bool { return c >= '0' && c <= '9' }

func (p *jsonParser) at() uint16 {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0xFFFF
}

func (p *jsonParser) parseNumber() any {
	start := p.pos
	if p.at() == '-' {
		p.pos++
		if !isDigit(p.at()) {
			p.failAt("No number after minus sign in JSON")
			return nil
		}
	}
	if p.at() == '0' {
		p.pos++
		if isDigit(p.at()) {
			p.failAt("Unexpected number in JSON")
			return nil
		}
	} else {
		for isDigit(p.at()) {
			p.pos++
		}
	}
	if p.at() == '.' {
		p.pos++
		if !isDigit(p.at()) {
			p.failAt("Unterminated fractional number in JSON")
			return nil
		}
		for isDigit(p.at()) {
			p.pos++
		}
	}
	if p.at() == 'e' || p.at() == 'E' {
		p.pos++
		if p.at() == '-' || p.at() == '+' {
			p.pos++
		}
		if !isDigit(p.at()) {
			p.failAt("Exponent part is missing a number in JSON")
			return nil
		}
		for isDigit(p.at()) {
			p.pos++
		}
	}
	text := make([]byte, p.pos-start)
	for i := range text {
		text[i] = byte(p.src[start+i])
	}
	f, err := strconv.ParseFloat(string(text), 64)
	if err != nil && !math.IsInf(f, 0) {
		p.failAt("Unexpected number in JSON")
		return nil
	}
	return f
}

func hexValue(c uint16) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func (p *jsonParser) parseString() any {
	p.pos++
	start := p.pos
	var buf []uint16
	for {
		if p.pos >= len(p.src) {
			p.failAt("Unterminated string in JSON")
			return nil
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			var s string
			if buf == nil {
				s = fromUnits(p.src[start:p.pos])
			} else {
				s = fromUnits(append(buf, p.src[start:p.pos]...))
			}
			p.pos++
			return s
		case c < 0x20:
			p.failAt("Bad control character in string literal in JSON")
			return nil
		case c == '\\':
			buf = append(buf, p.src[start:p.pos]...)
			p.pos++
			if p.pos >= len(p.src) {
				p.failToken()
				return nil
			}
			var out uint16
			switch p.src[p.pos] {
			case '"':
				out = '"'
			case '\\':
				out = '\\'
			case '/':
				out = '/'
			case 'b':
				out = '\b'
			case 'f':
				out = '\f'
			case 'n':
				out = '\n'
			case 'r':
				out = '\r'
			case 't':
				out = '\t'
			case 'u':
				var value uint16
				for i := 0; i < 4; i++ {
					p.pos++
					digit := -1
					if p.pos < len(p.src) {
						digit = hexValue(p.src[p.pos])
					}
					if digit < 0 {
						p.failAt("Bad Unicode escape in JSON")
						return nil
					}
					value = value<<4 | uint16(digit)
				}
				out = value
			default:
				p.failAt("Bad escaped character in JSON")
				return nil
			}
			buf = append(buf, out)
			p.pos++
			start = p.pos
		default:
			p.pos++
		}
	}
}

func (p *jsonParser) parseArray() any {
	if !p.enter() {
		return nil
	}
	defer func() { p.depth-- }()
	p.pos++
	out := []any{}
	p.skipSpace()
	if p.at() == ']' {
		p.pos++
		return out
	}
	for {
		v := p.parseValue()
		if p.err != nil {
			return nil
		}
		out = append(out, v)
		p.skipSpace()
		switch p.at() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return out
		default:
			p.failAt("Expected ',' or ']' after array element in JSON")
			return nil
		}
	}
}

func (p *jsonParser) parseObject() any {
	if !p.enter() {
		return nil
	}
	defer func() { p.depth-- }()
	p.pos++
	out := NewObject()
	p.skipSpace()
	if p.at() == '}' {
		p.pos++
		return out
	}
	if p.at() != '"' {
		p.failAt("Expected property name or '}' in JSON")
		return nil
	}
	for {
		key := p.parseString()
		if p.err != nil {
			return nil
		}
		p.skipSpace()
		if p.at() != ':' {
			p.failAt("Expected ':' after property name in JSON")
			return nil
		}
		p.pos++
		v := p.parseValue()
		if p.err != nil {
			return nil
		}
		out.Set(key.(string), v)
		p.skipSpace()
		switch p.at() {
		case ',':
			p.pos++
			p.skipSpace()
			if p.at() != '"' {
				p.failAt("Expected double-quoted property name in JSON")
				return nil
			}
		case '}':
			p.pos++
			out.sortKeys()
			return out
		default:
			p.failAt("Expected ',' or '}' after property value in JSON")
			return nil
		}
	}
}
