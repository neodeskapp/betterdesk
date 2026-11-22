package addressparser

// Kind and flags of a token.
const (
	// tokenOperator marks an operator; a token without it is a text run.
	tokenOperator = 1 << iota
	// tokenNoBreak says the next character continues the same run without
	// whitespace in between.
	tokenNoBreak
	// tokenDirty says the text run holds characters its value leaves out or
	// changes, so the value is no plain slice of the field.
	tokenDirty
	// tokenEscapes says a backslash escapes the next character in the text
	// run: the run sits inside a quoted string or a comment.
	tokenEscapes
	// tokenStartsEscaped says the text run opens on a character that a
	// backslash in front of it has escaped.
	tokenStartsEscaped
)

// token is an operator or a text run, kept as the range of the field it covers.
type token struct {
	kind  uint8
	start int
	end   int
}

// tokenList holds the tokens of an address field. The value of a text run is
// sliced off the field when it is read. The few runs whose value differs from
// their slice (an escaping backslash, a line break, a control character) are
// rebuilt once and kept in cleaned, by token index.
type tokenList struct {
	str     []rune
	tokens  []token
	cleaned map[int][]rune
}

// push appends a token and returns its index.
func (l *tokenList) push(kind uint8, start, end int) int {
	l.tokens = append(l.tokens, token{kind: kind, start: start, end: end})
	return len(l.tokens) - 1
}

// operatorAt returns the character of an operator token, 0 for a text run.
func (l *tokenList) operatorAt(index int) rune {
	t := l.tokens[index]
	if t.kind&tokenOperator != 0 {
		return l.str[t.start]
	}
	return 0
}

// isOperator reports whether a token is an operator.
func (l *tokenList) isOperator(index int) bool {
	return l.tokens[index].kind&tokenOperator != 0
}

// noBreak reports whether the character after a token continues its run
// without whitespace in between.
func (l *tokenList) noBreak(index int) bool {
	return l.tokens[index].kind&tokenNoBreak != 0
}

// value returns the value of a token. The slice has no spare capacity, so
// appending to it copies and leaves the field alone.
func (l *tokenList) value(index int) []rune {
	if cleaned, ok := l.cleaned[index]; ok {
		return cleaned
	}
	t := l.tokens[index]
	return l.str[t.start:t.end:t.end]
}

// tokenizer splits an address field into operators and text runs.
type tokenizer struct {
	str  []rune
	list tokenList

	// expecting is the character that closes the operator that is open, 0
	// when none is.
	expecting rune
	// node is the index of the text run being collected, -1 when there is none.
	node int
	// escaped says a backslash has escaped the character that comes next.
	escaped bool
	// inDomainLiteral says the walk is between the brackets of a domain
	// literal outside any operator, where a ':' is no group delimiter.
	inDomainLiteral bool
	// afterAt says the last character that counts was an '@', which is where
	// a '[' opens a domain literal.
	afterAt bool
	// inGroup says a ':' has opened a group that no ';' has closed.
	inGroup bool

	// What is open inside an angle-addr, which its '>' must not be read from.
	innerQuoted  bool
	innerComment int
	innerLiteral bool
	innerEscaped bool
}

// closerOf returns the character that ends the sequence an operator opens (0
// when the operator stands alone), and whether chr is an operator at all.
// The ';' is no legal separator outside a group, but mail clients have let
// people type it for a comma, so it is read as one. The ':' only marks where
// the name of a group ends.
func closerOf(chr rune) (closer rune, ok bool) {
	switch chr {
	case '"':
		return '"', true
	case '(':
		return ')', true
	case '<':
		return '>', true
	case ',', ':', ';':
		return 0, true
	}
	return 0, false
}

// tokenize splits the field into its tokens.
func tokenize(str []rune) *tokenList {
	t := &tokenizer{str: str, node: -1}
	t.list.str = str
	for i, chr := range str {
		t.checkChar(chr, i)
	}
	t.closeText(len(str))

	// Compacted in place: a text run is trimmed to its value and one left
	// empty is dropped, and the value of a run that is no plain slice is
	// rebuilt and kept aside.
	list := &t.list
	kept := 0
	for _, tok := range list.tokens {
		if tok.kind&tokenDirty != 0 {
			value := trimSpace(t.cleanValue(tok))
			if len(value) == 0 {
				continue
			}
			if list.cleaned == nil {
				list.cleaned = make(map[int][]rune)
			}
			list.cleaned[kept] = value
		} else if tok.kind&tokenOperator == 0 {
			for tok.start < tok.end && isSpace(str[tok.start]) {
				tok.start++
			}
			for tok.end > tok.start && isSpace(str[tok.end-1]) {
				tok.end--
			}
			if tok.start == tok.end {
				continue
			}
		}
		list.tokens[kept] = tok
		kept++
	}
	list.tokens = list.tokens[:kept]
	return list
}

// closeText ends the text run being collected, if there is one, at offset end.
func (t *tokenizer) closeText(end int) {
	if t.node >= 0 {
		t.list.tokens[t.node].end = end
		t.node = -1
	}
}

// markDirty flags the text run being collected as no plain slice of the field.
func (t *tokenizer) markDirty() {
	t.list.tokens[t.node].kind |= tokenDirty
}

// cleanValue rebuilds the value of a text run that is no plain slice of the
// field: an escaping backslash is dropped, a line feed reads as a space and
// any other control character is left out.
func (t *tokenizer) cleanValue(tok token) []rune {
	escapes := tok.kind&tokenEscapes != 0
	escaped := tok.kind&tokenStartsEscaped != 0
	value := make([]rune, 0, tok.end-tok.start)
	for _, chr := range t.str[tok.start:tok.end] {
		if escapes && !escaped && chr == '\\' {
			escaped = true
			continue
		}
		escaped = false
		if chr == '\n' {
			value = append(value, ' ')
		} else if chr >= 0x21 || chr == ' ' || chr == '\t' {
			value = append(value, chr)
		}
	}
	return value
}

// checkChar reads the character at offset i: it opens or closes an operator,
// or it belongs to a text run.
func (t *tokenizer) checkChar(chr rune, i int) {
	// A domain literal opens at a '[' right after the '@' of an addr-spec and
	// outside any operator. All it hides is the ':' (as in an IPv6 literal),
	// which is no group delimiter in there. A ']' ends it, and so do the list
	// separators, so that an unclosed '[' cannot swallow later recipients.
	if !t.escaped && t.expecting == 0 {
		if !t.inDomainLiteral && chr == '[' && t.afterAt {
			t.inDomainLiteral = true
		} else if t.inDomainLiteral && (chr == ']' || chr == ',' || chr == ';') {
			t.inDomainLiteral = false
		}
	}

	// The '>' of an angle-addr is only read outside the quoted strings,
	// comments and domain literals it holds, and the ')' of a comment only
	// when no comment nested in it is open.
	nested := false
	if t.expecting == '>' {
		nested = t.trackInner(chr)
		if !nested && chr == ';' && t.inGroup {
			// the ';' of a group ends an angle-addr left open inside it
			t.closeText(i)
			t.expecting = 0
			t.resetInner()
		}
	} else if t.expecting == ')' && !t.escaped {
		if chr == '(' {
			t.innerComment++
		} else if chr == ')' && t.innerComment > 0 {
			t.innerComment--
			nested = true
		}
	}

	switch {
	case t.escaped:
		// an escaped character is text, whatever it is
	case t.expecting != 0 && chr == t.expecting && !nested:
		kind := uint8(tokenOperator)
		if i+1 < len(t.str) {
			switch t.str[i+1] {
			case ' ', '\t', '\r', '\n', ',', ';':
			default:
				kind |= tokenNoBreak
			}
		}
		t.closeText(i)
		t.list.push(kind, i, i+1)
		if chr != ')' {
			// a comment is folding whitespace, it does not part the '@' from the '['
			t.afterAt = false
		}
		t.expecting = 0
		t.escaped = false
		t.resetInner()
		return
	case t.expecting == 0 && !(t.inDomainLiteral && chr == ':'):
		closer, isOperator := closerOf(chr)
		if !isOperator {
			break
		}
		t.closeText(i)
		t.list.push(tokenOperator, i, i+1)
		if chr == ':' {
			t.inGroup = true
		} else if chr == ';' {
			t.inGroup = false
		}
		if chr != '(' {
			t.afterAt = false
		}
		t.expecting = closer
		t.escaped = false
		t.resetInner()
		return
	case (t.expecting == '"' || t.expecting == ')') && chr == '\\':
		// a backslash escapes the next character inside a quoted string and
		// inside a comment, and is left out of the value
		t.escaped = true
		if t.node >= 0 {
			t.markDirty()
		}
		return
	}

	if t.node < 0 {
		var kind uint8
		if t.expecting == '"' || t.expecting == ')' {
			kind |= tokenEscapes
		}
		if t.escaped {
			kind |= tokenStartsEscaped
		}
		t.node = t.list.push(kind, i, i)
	}
	if chr == '\n' {
		// a line feed reads as a space; a carriage return is a control
		// character and is left out
		chr = ' '
		t.markDirty()
	}
	if chr >= 0x21 || chr == ' ' || chr == '\t' {
		// the character is part of the value; text inside a quoted string or
		// a comment is not part of an addr-spec
		if t.expecting == 0 && chr != ' ' && chr != '\t' {
			t.afterAt = chr == '@'
		}
	} else {
		t.markDirty()
	}
	t.escaped = false
}

// trackInner follows the quoted strings, comments and domain literals inside
// an angle-addr. It returns true while the character sits inside one of them,
// the one that closes it included. A backslash escapes the next character
// inside a quoted string and a comment. A domain literal opens right after an
// '@' and ends at the ']'.
func (t *tokenizer) trackInner(chr rune) bool {
	if t.innerEscaped {
		t.innerEscaped = false
		return true
	}
	if t.innerQuoted {
		if chr == '\\' {
			t.innerEscaped = true
		} else if chr == '"' {
			t.innerQuoted = false
		}
		return true
	}
	if t.innerComment > 0 {
		switch chr {
		case '\\':
			t.innerEscaped = true
		case '(':
			t.innerComment++
		case ')':
			t.innerComment--
		}
		return true
	}
	if t.innerLiteral {
		if chr == ']' {
			t.innerLiteral = false
		}
		return true
	}
	if chr == '"' {
		t.innerQuoted = true
		return true
	}
	if chr == '(' {
		t.innerComment = 1
		return true
	}
	if chr == '[' && t.afterAt {
		t.innerLiteral = true
		return true
	}
	// inside an angle-addr afterAt serves the literal tracked here
	if !isSpace(chr) {
		t.afterAt = chr == '@'
	}
	return false
}

// resetInner forgets the nesting collected for the operator that just opened
// or ended.
func (t *tokenizer) resetInner() {
	t.innerQuoted = false
	t.innerComment = 0
	t.innerLiteral = false
	t.innerEscaped = false
}
