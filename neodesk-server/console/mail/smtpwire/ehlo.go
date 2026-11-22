package smtpwire

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ehloInfo is what a session reads out of an EHLO reply.
type ehloInfo struct {
	// lines are the reply lines after the first, without code and separator
	lines []string
	// extensions the session acts on
	smtpUTF8, eightBitMIME, pipelining, size bool
	// maxSize is the number after SIZE, 0 when the server named none
	maxSize float64
	// allowsAuth says the reply mentions AUTH, mechanisms lists the ones
	// that can be used, in the order they are tried
	allowsAuth bool
	mechanisms []string
}

// hasKeyword reports whether an EHLO reply contains the keyword (given in
// upper case) after a space or a dash and before something that is not a
// letter, digit or underscore. The first line counts too, and so does a
// keyword in the middle of a line.
func hasKeyword(reply, keyword string) bool {
	return keywordEnd(reply, keyword, true) >= 0
}

// keywordEnd finds the first place where reply has a space or dash followed
// by keyword in any case, and returns the index after it. With boundary the
// keyword must not go on with a letter, digit or underscore.
func keywordEnd(reply, keyword string, boundary bool) int {
	for i := 0; i+1+len(keyword) <= len(reply); i++ {
		if reply[i] != ' ' && reply[i] != '-' {
			continue
		}
		if !hasPrefixFold(reply[i+1:], keyword) {
			continue
		}
		end := i + 1 + len(keyword)
		if boundary && end < len(reply) && isWordByte(reply[end]) {
			continue
		}
		return end
	}
	return -1
}

// parseEhlo reads the extensions and login mechanisms out of an EHLO reply
// (lines joined with "\n").
func parseEhlo(reply string) ehloInfo {
	var info ehloInfo
	for _, line := range splitLines(reply) {
		if line = jsTrim(stripReplyCode(line)); line != "" {
			info.lines = append(info.lines, line)
		}
	}
	if len(info.lines) > 1 {
		info.lines = info.lines[1:]
	} else {
		info.lines = nil
	}
	info.smtpUTF8 = hasKeyword(reply, "SMTPUTF8")
	info.eightBitMIME = hasKeyword(reply, "8BITMIME")
	info.pipelining = hasKeyword(reply, "PIPELINING")
	info.allowsAuth = hasKeyword(reply, "AUTH")

	offered := map[string]bool{}
	for _, line := range info.lines {
		list, ok := authList(line)
		if !ok {
			continue
		}
		for _, mechanism := range strings.FieldsFunc(list, func(r rune) bool { return r == '=' || isJSSpace(r) }) {
			offered[strings.ToUpper(mechanism)] = true
		}
	}
	for _, mechanism := range []string{"PLAIN", "LOGIN", "CRAM-MD5"} {
		if offered[mechanism] {
			info.mechanisms = append(info.mechanisms, mechanism)
		}
	}

	if end := keywordEnd(reply, "SIZE", false); end >= 0 {
		info.size = true
		rest := reply[end:]
		blanks := 0
		for blanks < len(rest) && (rest[blanks] == ' ' || rest[blanks] == '\t') {
			blanks++
		}
		digits := blanks
		for digits < len(rest) && isDigit(rest[digits]) {
			digits++
		}
		if blanks > 0 && digits > blanks {
			// a number too large for a float64 parses as infinity, which is
			// what JavaScript makes of it
			info.maxSize, _ = strconv.ParseFloat(rest[blanks:digits], 64)
		}
	}
	return info
}

// splitLines splits at "\n" and drops one "\r" before it.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, line := range lines[:len(lines)-1] {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// stripReplyCode removes the digits and the separator a reply line starts
// with.
func stripReplyCode(line string) string {
	i := 0
	for i < len(line) && isDigit(line[i]) {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == ' ' || line[i] == '-') {
		return line[i+1:]
	}
	return line
}

// authList returns the mechanism list of an "AUTH ..." or "AUTH=..." line:
// what follows the separator up to the end of the line.
func authList(line string) (string, bool) {
	if !hasPrefixFold(line, "AUTH") {
		return "", false
	}
	rest := line[len("AUTH"):]
	separator, size := utf8.DecodeRuneInString(rest)
	if size == 0 || (separator != '=' && !isJSSpace(separator)) {
		return "", false
	}
	rest = rest[size:]
	if end := strings.IndexFunc(rest, isJSLineEnd); end >= 0 {
		rest = rest[:end]
	}
	return rest, true
}
