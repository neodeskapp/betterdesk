package addressparser

// The parser works on the field as a slice of code points, one element per
// character. Nothing in it tells the two halves of a UTF-16 surrogate pair
// apart from any other ordinary character (they are no whitespace, no word
// character, no operator and no control character), so a slice of code
// points gives the result a walk over UTF-16 code units gives.

// isSpace reports whether c is whitespace as the JavaScript pattern class \s
// and String.prototype.trim define it.
func isSpace(c rune) bool {
	return c == 0x20 ||
		(c >= 0x09 && c <= 0x0d) ||
		c == 0xa0 ||
		c == 0x1680 ||
		(c >= 0x2000 && c <= 0x200a) ||
		c == 0x2028 ||
		c == 0x2029 ||
		c == 0x202f ||
		c == 0x205f ||
		c == 0x3000 ||
		c == 0xfeff
}

// isWord reports whether c is a word character: an ASCII letter, an ASCII
// digit or the underscore.
func isWord(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

// isBoundary reports whether a word boundary sits at offset at: exactly one
// of the characters on its two sides is a word character. A position off
// either end of the text counts as no word character.
func isBoundary(text []rune, at int) bool {
	before := at >= 1 && at-1 < len(text) && isWord(text[at-1])
	after := at >= 0 && at < len(text) && isWord(text[at])
	return before != after
}

// trimSpace returns text without the whitespace at its two ends.
func trimSpace(text []rune) []rune {
	start, end := 0, len(text)
	for start < end && isSpace(text[start]) {
		start++
	}
	for end > start && isSpace(text[end-1]) {
		end--
	}
	return text[start:end:end]
}

// hasSpace reports whether text holds whitespace anywhere.
func hasSpace(text []rune) bool {
	for _, c := range text {
		if isSpace(c) {
			return true
		}
	}
	return false
}

// indexFrom returns the offset of the first c in text at or after from, or -1.
func indexFrom(text []rune, c rune, from int) int {
	for i := max(from, 0); i < len(text); i++ {
		if text[i] == c {
			return i
		}
	}
	return -1
}

// lastIndex returns the offset of the last c in text, or -1.
func lastIndex(text []rune, c rune) int {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == c {
			return i
		}
	}
	return -1
}

// equal reports whether two texts hold the same characters.
func equal(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// join puts a single space between the parts.
func join(parts [][]rune) []rune {
	size := 0
	for _, part := range parts {
		size += len(part) + 1
	}
	joined := make([]rune, 0, size)
	for i, part := range parts {
		if i > 0 {
			joined = append(joined, ' ')
		}
		joined = append(joined, part...)
	}
	return joined
}

// quotedPrefix returns the length of the quoted string text opens with, the
// two quotes included, or -1 when it opens with none. Inside the quotes a
// backslash takes the next character along, so an escaped quote does not end
// the string, and a string whose closing quote is missing is none.
func quotedPrefix(text []rune) int {
	if len(text) == 0 || text[0] != '"' {
		return -1
	}
	for i := 1; i < len(text); {
		switch text[i] {
		case '"':
			return i + 1
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return -1
}

// isLiteralBody reports whether text can stand between the brackets of a
// domain literal in an address that holds no whitespace.
func isLiteralBody(text []rune) bool {
	for _, c := range text {
		if c == '[' || c == ']' || c == '\\' || isSpace(c) {
			return false
		}
	}
	return true
}

// isAddrSpec reports whether a run reads as an addr-spec under the strict
// grammar: no whitespace, one '@' with something on both sides, or one '@'
// with something in front and a domain literal behind it, which may hold an
// '@' of its own.
func isAddrSpec(text []rune) bool {
	at := -1
	for i, c := range text {
		if isSpace(c) {
			return false
		}
		if c == '@' && at < 0 {
			at = i
		}
	}
	if at < 1 {
		return false
	}
	domain := text[at+1:]
	if len(domain) > 0 && indexFrom(domain, '@', 0) < 0 {
		return true
	}
	return len(domain) >= 2 && domain[0] == '[' && domain[len(domain)-1] == ']' && isLiteralBody(domain[1:len(domain)-1])
}

// isLooseAddrSpec is the looser reading: no whitespace, something in front of
// the first '@' and something behind it, further '@' included.
func isLooseAddrSpec(text []rune) bool {
	if hasSpace(text) {
		return false
	}
	at := indexFrom(text, '@', 0)
	return at >= 1 && at < len(text)-1
}

// isQuotedLocalDomain reports whether text is the '@' and the domain that
// follow a quoted local part: an '@' and then either a run without '@',
// whitespace and quotes, or a domain literal.
func isQuotedLocalDomain(text []rune) bool {
	if len(text) < 2 || text[0] != '@' {
		return false
	}
	domain := text[1:]
	plain := true
	for _, c := range domain {
		if c == '@' || c == '"' || isSpace(c) {
			plain = false
			break
		}
	}
	if plain {
		return true
	}
	return len(domain) >= 2 && domain[0] == '[' && domain[len(domain)-1] == ']' && isLiteralBody(domain[1:len(domain)-1])
}

// isUnambiguousLocalPart reports whether a local part reads the same with or
// without quotes around it: it is not empty and holds no whitespace and none
// of the specials.
func isUnambiguousLocalPart(user []rune) bool {
	if len(user) == 0 {
		return false
	}
	for _, c := range user {
		switch c {
		case '"', '(', ')', ',', ':', ';', '<', '>', '@', '[', '\\', ']':
			return false
		}
		if isSpace(c) {
			return false
		}
	}
	return true
}

// splitQuotedLocalAddr reads an address that opens with a quoted local part:
// the quoted string, an '@' and the run up to the next whitespace are the
// addr-spec. What follows the whitespace after it is the rest, and hasRest
// says whether there is one. ok is false when the text has another shape,
// which includes an addr-spec followed by a single whitespace character and
// nothing else.
func splitQuotedLocalAddr(text []rune) (addr, rest []rune, hasRest, ok bool) {
	quoted := quotedPrefix(text)
	if quoted < 0 || quoted >= len(text) || text[quoted] != '@' {
		return nil, nil, false, false
	}
	end := quoted + 1
	for end < len(text) && !isSpace(text[end]) {
		end++
	}
	if end == quoted+1 {
		return nil, nil, false, false
	}
	if end == len(text) {
		return text, nil, false, true
	}
	next := end
	for next < len(text) && isSpace(text[next]) {
		next++
	}
	if next < len(text) {
		return text[:end:end], text[next:], true, true
	}
	if next-end >= 2 {
		// whitespace runs to the end: all but its last character parts the
		// addr-spec from the rest, and the last one is the rest
		return text[:end:end], text[len(text)-1:], true, true
	}
	return nil, nil, false, false
}

// matchLooseTextAddr reads an addr-spec sitting in free text at offset at,
// together with the whitespace around it, and returns the offset just past
// the match. The match is: whitespace, a word boundary, a run without '@'
// and whitespace, an '@', and the rest of that whitespace delimited run up to
// the last word boundary in it, then the whitespace that follows.
func matchLooseTextAddr(text []rune, at int) (end int, ok bool) {
	n := len(text)
	start := at
	for start < n && isSpace(text[start]) {
		start++
	}
	if !isBoundary(text, start) {
		return 0, false
	}
	atSign := start
	for atSign < n && text[atSign] != '@' && !isSpace(text[atSign]) {
		atSign++
	}
	if atSign == start || atSign >= n || text[atSign] != '@' {
		return 0, false
	}
	runEnd := atSign + 1
	for runEnd < n && !isSpace(text[runEnd]) {
		runEnd++
	}
	end = runEnd
	for end > atSign+1 && !isBoundary(text, end) {
		end--
	}
	if end <= atSign+1 {
		return 0, false
	}
	for end < n && isSpace(text[end]) {
		end++
	}
	return end, true
}

// looseAddressStart finds the offset matchLooseTextAddr matches at when the
// text is searched from its start, or -1 when it matches nowhere. A match
// can only begin at the head of a whitespace delimited run or just past an
// '@' inside one, and it can only end at the last word boundary of that run,
// so one pass over the text finds it.
func looseAddressStart(text []rune) int {
	n := len(text)
	pos := 0
	for pos < n {
		for pos < n && isSpace(text[pos]) {
			pos++
		}
		if pos >= n {
			break
		}
		runStart := pos
		runEnd := pos
		for runEnd < n && !isSpace(text[runEnd]) {
			runEnd++
		}
		at := indexWithin(text, '@', runStart, runEnd)
		if at >= 0 {
			lastBoundary := -1
			for k := runEnd; k > runStart; k-- {
				if isBoundary(text, k) {
					lastBoundary = k
					break
				}
			}
			atomStart := runStart
			for lastBoundary >= 0 && at >= 0 {
				// the run in front of the '@' and the run behind it both need a
				// character, and the boundary that ends the match sits past both
				if at > atomStart && runEnd > at+1 && lastBoundary > at+1 {
					for start := atomStart; start < at; start++ {
						if isBoundary(text, start) {
							if start > runStart {
								return start
							}
							// a match that begins at the head of the run takes the
							// whitespace in front of it along
							padded := runStart
							for padded > 0 && isSpace(text[padded-1]) {
								padded--
							}
							return padded
						}
					}
				}
				atomStart = at + 1
				at = indexWithin(text, '@', atomStart, runEnd)
			}
		}
		pos = runEnd
	}
	return -1
}

// indexWithin returns the offset of the first c in text[from:to], or -1. It
// never looks past to, so a walk that calls it once per run stays linear.
func indexWithin(text []rune, c rune, from, to int) int {
	for i := from; i < to; i++ {
		if text[i] == c {
			return i
		}
	}
	return -1
}
