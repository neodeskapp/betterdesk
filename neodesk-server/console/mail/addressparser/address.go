package addressparser

// none stands for "no character" where a character is remembered.
const none rune = -1

// domainAt returns the offset of the '@' that splits the domain off an
// address, or -1 when there is none. That is the last '@', unless the
// address ends in a domain literal: its text may hold an '@' of its own, so
// the '@' in front of the literal is the one.
func domainAt(address []rune) int {
	n := len(address)
	if n > 0 && address[n-1] == ']' {
		open := n - 2
		for open >= 0 && address[open] != '[' && address[open] != ']' && address[open] != '\\' {
			open--
		}
		if open >= 1 && address[open] == '[' && address[open-1] == '@' {
			return open - 1
		}
	}
	return lastIndex(address, '@')
}

// quoteLocalPart puts the quotes back on a local part that was read out of a
// quoted string. A local part that holds no special reads the same without
// them and stays bare, and one that is a complete quoted string already
// stays as it is. Without the quotes it would be up to the reader of
// 'user@evil.com@good.com' which '@' splits the domain off.
func quoteLocalPart(address []rune) []rune {
	lastAt := domainAt(address)
	if lastAt < 0 {
		return address
	}
	user := address[:lastAt]
	if isUnambiguousLocalPart(user) || quotedPrefix(user) == len(user) {
		return address
	}
	quoted := make([]rune, 0, len(address)+len(user)+2)
	quoted = append(quoted, '"')
	for _, c := range user {
		if c == '"' || c == '\\' {
			quoted = append(quoted, '\\')
		}
		quoted = append(quoted, c)
	}
	quoted = append(quoted, '"', '@')
	return append(quoted, address[lastAt+1:]...)
}

// joinAtSpacing drops the whitespace on both sides of the '@' at offset at.
// A negative offset leaves the value alone.
func joinAtSpacing(value []rune, at int) []rune {
	if at < 0 {
		return value
	}
	start := at
	for start > 0 && isSpace(value[start-1]) {
		start--
	}
	end := at + 1
	for end < len(value) && isSpace(value[end]) {
		end++
	}
	if start == at && end == at+1 {
		return value
	}
	joined := make([]rune, 0, len(value))
	joined = append(joined, value[:start]...)
	joined = append(joined, '@')
	return append(joined, value[end:]...)
}

// collapseAtSpacing drops the whitespace beside the '@' that splits the
// domain off a value: the '@' right after a quoted local part the value
// opens with, or else the only '@' it holds, as long as no quote follows it.
// With several '@' it is not clear which one the whitespace belongs to, and
// the value stays as it is.
func collapseAtSpacing(value []rune) []rune {
	if at := quotedPrefix(value); at >= 0 {
		for at < len(value) && isSpace(value[at]) {
			at++
		}
		if at < len(value) && value[at] == '@' && indexFrom(value, '@', at+1) < 0 {
			return joinAtSpacing(value, at)
		}
	}
	at := indexFrom(value, '@', 0)
	if at < 0 || indexFrom(value, '@', at+1) >= 0 || lastIndex(value, '"') > at {
		return value
	}
	return joinAtSpacing(value, at)
}

// splitSpace cuts text at every run of whitespace. Whitespace at the start
// or the end leaves an empty part there.
func splitSpace(text []rune) [][]rune {
	var parts [][]rune
	start := 0
	for i := 0; i < len(text); {
		if !isSpace(text[i]) {
			i++
			continue
		}
		parts = append(parts, text[start:i:i])
		for i < len(text) && isSpace(text[i]) {
			i++
		}
		start = i
	}
	return append(parts, text[start:])
}

// mailbox is what the walk over one entry of the list has collected so far:
// the address and the display text.
type mailbox struct {
	address []rune
	text    []rune
}

// recoverAddrSpec picks the addr-spec out of an address that holds unquoted
// whitespace, which happens when a malformed header puts more than a mailbox
// between the angle brackets. The run that reads as an addr-spec stays the
// address (strict reading first, then the loose one; the first run wins) and
// the other runs go to the display text. Whitespace inside a quoted local
// part is legal and stays. An address with a quote that is no well formed
// quoted local part stays as it is.
func recoverAddrSpec(data *mailbox) {
	data.address = collapseAtSpacing(data.address)
	if !hasSpace(data.address) {
		return
	}
	var address []rune
	var rest [][]rune
	if addr, tail, hasTail, ok := splitQuotedLocalAddr(data.address); ok {
		if !hasTail {
			return
		}
		address = addr
		rest = [][]rune{tail}
	} else {
		if indexFrom(data.address, '"', 0) >= 0 {
			return
		}
		parts := splitSpace(data.address)
		addrIndex := -1
		for i, part := range parts {
			if isAddrSpec(part) {
				addrIndex = i
				break
			}
		}
		if addrIndex < 0 {
			for i, part := range parts {
				if isLooseAddrSpec(part) {
					addrIndex = i
					break
				}
			}
		}
		if addrIndex < 0 {
			return
		}
		address = parts[addrIndex]
		rest = append(parts[:addrIndex:addrIndex], parts[addrIndex+1:]...)
	}
	data.address = address
	kept := make([][]rune, 0, len(rest)+1)
	if len(data.text) > 0 {
		kept = append(kept, data.text)
	}
	for _, part := range rest {
		if len(part) > 0 {
			kept = append(kept, part)
		}
	}
	data.text = join(kept)
}

// stripAddressComments takes the comments out of the contents of an
// angle-addr and returns what is left, trimmed, and the texts of the
// comments that are not empty.
//
// A comment stands for a space that parts two atoms and for nothing beside
// an '@'. Quoted strings and domain literals are copied through, a '(' in
// there opens no comment. A '<' outside them drops everything in front of
// it, comments included. Contents that start with '@' are a route up to the
// first ':' outside a domain literal and in front of any quoted string; the
// route is dropped when an '@' follows it.
func stripAddressComments(address []rune) (stripped []rune, comments [][]rune) {
	result := make([]rune, 0, len(address))
	var comment []rune
	depth := 0
	var closer rune
	// last character written to the result
	lastChar := none
	// A comment that just closed stands for a space, which is written once
	// the next character shows whether one is wanted: not beside an '@' and
	// not on top of whitespace that follows.
	pendingSpace := false
	// first character of the result that is no whitespace, and whether a
	// quoted string has opened since: together they tell a route from a
	// local part
	firstChar := none
	quoteSeen := false
	routeEnd := -1

	n := len(address)
	for i := 0; i < n; i++ {
		chr := address[i]
		if depth > 0 {
			switch {
			case chr == '\\' && i < n-1:
				i++
				comment = append(comment, address[i])
			case chr == '(':
				depth++
				comment = append(comment, chr)
			case chr == ')' && depth == 1:
				depth = 0
				comments = append(comments, trimSpace(comment))
				comment = nil
				pendingSpace = lastChar != '@'
			case chr == ')':
				depth--
				comment = append(comment, chr)
			default:
				comment = append(comment, chr)
			}
			continue
		}
		if pendingSpace && chr != '(' {
			pendingSpace = false
			if chr != '@' && chr != '<' && !isSpace(chr) {
				result = append(result, ' ')
				lastChar = ' '
			}
		}
		switch {
		case closer != 0:
			if chr == '\\' && closer == '"' && i < n-1 {
				i++
				result = append(result, chr, address[i])
				lastChar = address[i]
				continue
			}
			if chr == closer {
				closer = 0
			}
		case chr == '"':
			closer = '"'
			quoteSeen = true
		case chr == ':' && routeEnd < 0 && firstChar == '@' && !quoteSeen:
			routeEnd = len(result) + 1
		case chr == '[':
			closer = ']'
		case chr == '(':
			depth = 1
			continue
		case chr == '<':
			result = result[:0]
			lastChar = none
			comments = comments[:0]
			firstChar = none
			quoteSeen = false
			routeEnd = -1
			continue
		}
		result = append(result, chr)
		lastChar = chr
		if firstChar == none && !isSpace(chr) {
			firstChar = chr
		}
	}
	if routeEnd >= 0 && indexFrom(result, '@', routeEnd) >= 0 {
		result = result[routeEnd:]
	}
	if depth > 0 {
		// an unterminated comment runs to the end of the address
		comments = append(comments, trimSpace(comment))
	}
	kept := comments[:0]
	for _, text := range comments {
		if len(text) > 0 {
			kept = append(kept, text)
		}
	}
	return trimSpace(result), kept
}

// The runs an entry of the list is sorted into.
const (
	stateText = iota
	stateAddress
	stateComment
)

// addressParts are the runs of one entry of an address list.
type addressParts struct {
	// runs holds the display text, the contents of the angle brackets and
	// the comments, by state.
	runs [3][][]rune
	// textWasQuoted says for every text run whether part of it came out of a
	// quoted string.
	textWasQuoted []bool
	// quotedLocalAt is, for every text run, the offset of the '@' that
	// follows a quoted local part the run opens with, or -1.
	quotedLocalAt []int
}

// collectAddressParts walks the tokens of one entry of an address list, from
// index from up to index to, and sorts them into runs of display text,
// angle-addr and comment. textFromComment says the display text came out of
// the comments because there was no other.
func collectAddressParts(tokens *tokenList, from, to int) (data *addressParts, textFromComment bool) {
	state := stateText
	data = &addressParts{}
	insideQuotes := false
	// whether the last text run holds nothing but quoted text so far, which
	// is what a quoted local part is until the '@' after it joins the run
	lastTextPureQuoted := false
	// last character of the run each state is collecting
	lastChars := [3]rune{none, none, none}

	for i := from; i < to; i++ {
		value := tokens.value(i)
		// the operator in front of this token, if there is one, and whether
		// it runs on into it
		var prevOperator rune
		prevNoBreak := false
		if i > from {
			prevOperator = tokens.operatorAt(i - 1)
			prevNoBreak = tokens.noBreak(i - 1)
		}
		if tokens.isOperator(i) {
			switch value[0] {
			case '<':
				state = stateAddress
				insideQuotes = false
			case '(':
				state = stateComment
				insideQuotes = false
			case '"':
				insideQuotes = !insideQuotes
				state = stateText
			default:
				state = stateText
				insideQuotes = false
			}
			continue
		}

		first, last := none, none
		if len(value) > 0 {
			first, last = value[0], value[len(value)-1]
		}
		// The tokenizer drops an empty quoted string, so the pair of quotes
		// right in front of this token is all that is left of it, and the run
		// this token opens carries the quoting instead.
		opensAfterEmptyQuotedString := prevOperator == '"' && prevNoBreak && i > from+1 && tokens.operatorAt(i-2) == '"'
		// A comment is folding whitespace. It may sit beside the '@' of an
		// addr-spec, but it does not glue two atoms into one.
		runs := data.runs[state]
		joins := prevNoBreak && len(runs) > 0 && (prevOperator != ')' || lastChars[state] == '@' || first == '@')
		if joins {
			at := len(runs) - 1
			if state == stateText && !insideQuotes {
				if lastTextPureQuoted && first == '@' {
					data.quotedLocalAt[at] = len(runs[at])
				}
				lastTextPureQuoted = false
			}
			runs[at] = append(runs[at], value...)
			lastChars[state] = last
			if state == stateText && insideQuotes {
				data.textWasQuoted[at] = true
			}
			continue
		}
		data.runs[state] = append(runs, value)
		lastChars[state] = last
		if state == stateText {
			data.textWasQuoted = append(data.textWasQuoted, insideQuotes || opensAfterEmptyQuotedString)
			// an empty quoted string leaves no text of its own, so the '@' opens the run
			local := -1
			if opensAfterEmptyQuotedString && first == '@' {
				local = 0
			}
			data.quotedLocalAt = append(data.quotedLocalAt, local)
			lastTextPureQuoted = insideQuotes
		}
	}

	// With no text but a comment, the comment is the text. Such text only
	// ever names the mailbox, no address is read out of it.
	if len(data.runs[stateText]) == 0 && len(data.runs[stateComment]) > 0 {
		data.runs[stateText] = data.runs[stateComment]
		data.runs[stateComment] = nil
		textFromComment = true
	}
	return data, textFromComment
}

// groupName is the name of a group, from the tokens in front of its ':'. A
// comment names it when no text does.
func groupName(tokens *tokenList, from, to int) string {
	data, _ := collectAddressParts(tokens, from, to)
	return string(join(data.runs[stateText]))
}

// handleAddress turns the tokens of one entry of the list, from index from
// up to index to, into a mailbox.
func handleAddress(tokens *tokenList, from, to int) Address {
	data, textFromComment := collectAddressParts(tokens, from, to)

	// Comments come out of the angle-addr first, so that brackets holding
	// nothing but a comment read the same as empty ones.
	var addressComments [][]rune
	var addresses [][]rune
	for _, part := range data.runs[stateAddress] {
		stripped, comments := stripAddressComments(part)
		addressComments = append(addressComments, comments...)
		if len(stripped) > 0 {
			addresses = append(addresses, stripped)
		}
	}
	texts := data.runs[stateText]

	// Without an angle-addr the address is looked for in the text.
	if len(addresses) == 0 && len(texts) > 0 && !textFromComment {
		// Whitespace beside the '@' is folding whitespace, so 'user @ good.com'
		// is read as one run. Only the run an address comes out of is replaced
		// by its collapsed form, the display text keeps its spacing.
		candidates := make([][]rune, len(texts))
		for i, part := range texts {
			candidates[i] = collapseAtSpacing(part)
		}
		// The first addr-spec in the text is the mailbox. No address is taken
		// out of a quoted string, but a run that is a quoted local part with
		// its '@' and domain is an addr-spec of its own, with the quotes put
		// back on.
		for i := range texts {
			var found []rune
			local := data.quotedLocalAt[i]
			if data.textWasQuoted[i] && local >= 0 && isQuotedLocalDomain(texts[i][local:]) {
				found = quoteLocalPart(texts[i])
			}
			if len(found) == 0 && !data.textWasQuoted[i] && isAddrSpec(candidates[i]) {
				found = candidates[i]
			}
			if len(found) > 0 {
				addresses = [][]rune{found}
				texts = append(texts[:i:i], texts[i+1:]...)
				data.textWasQuoted = append(data.textWasQuoted[:i:i], data.textWasQuoted[i+1:]...)
				data.quotedLocalAt = append(data.quotedLocalAt[:i:i], data.quotedLocalAt[i+1:]...)
				break
			}
		}
		// The looser reading, when the strict one found nothing: the first
		// unquoted run that holds an addr-spec gives it up and keeps the rest.
		if len(addresses) == 0 {
			for i := range texts {
				if data.textWasQuoted[i] {
					continue
				}
				part := candidates[i]
				remainder := texts[i]
				extracted := false
				if at := looseAddressStart(part); at >= 0 {
					if end, ok := matchLooseTextAddr(part, at); ok {
						addresses = [][]rune{trimSpace(part[at:end])}
						extracted = true
						remainder = make([]rune, 0, len(part)+1)
						remainder = append(remainder, part[:at]...)
						remainder = append(remainder, ' ')
						remainder = append(remainder, part[end:]...)
					}
				}
				texts[i] = trimSpace(remainder)
				if extracted {
					break
				}
			}
		}
	}

	// If there is still no text but a comment, the comment is the text.
	if len(texts) == 0 && len(data.runs[stateComment]) > 0 {
		texts = data.runs[stateComment]
	}

	// Only the first angle-addr is the address, the others are text.
	if len(addresses) > 1 {
		texts = append(texts[:len(texts):len(texts)], addresses[1:]...)
		addresses = addresses[:1]
	}

	// An address is only taken from unquoted text, so text that still has to
	// serve as the address carries its quoting in this flag.
	addressFromQuotedText := false
	if len(addresses) == 0 {
		for _, wasQuoted := range data.textWasQuoted {
			if wasQuoted {
				addressFromQuotedText = true
				break
			}
		}
	}

	result := &mailbox{text: join(texts), address: join(addresses)}
	if addressFromQuotedText && indexFrom(result.text, '@', 0) >= 0 {
		// A quoted run without an '@' is a display name and stays where it
		// is. With an '@' the mailbox is sitting in the text: it moves over
		// with the quotes back on its local part, so that the recovery below
		// can tell the whitespace a quoted local part may hold from wreckage
		// trailing the addr-spec. The quotes are gone at this point, so the
		// '@' the domain splits off at is the last one.
		result.address = quoteLocalPart(joinAtSpacing(result.text, domainAt(result.text)))
		result.text = nil
	}
	recoverAddrSpec(result)

	// a comment inside the brackets names the mailbox only when nothing else does
	if len(result.text) == 0 && len(addressComments) > 0 {
		result.text = join(addressComments)
		textFromComment = true
	}

	address := result.address
	if len(address) == 0 && !textFromComment {
		address = result.text
	}
	name := result.text
	if len(name) == 0 {
		name = result.address
	}
	if equal(address, name) {
		if indexFrom(address, '@', 0) >= 0 {
			name = nil
		} else {
			address = nil
		}
	}
	return Address{Name: string(name), Address: string(address)}
}
