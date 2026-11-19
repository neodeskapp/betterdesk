package mail

import "strings"

// PlainAddressList reports whether text is a list of plain addresses
// separated by commas, the form an alert rule's notify_emails and the
// alert address are saved in. Each entry is local@domain and nothing
// else: no display name, group, quotes, comment, white space inside it,
// line break or other control character. The local part is a dot-atom
// (letters, digits and !#$%&'*+-/=?^_`{|}~ in parts joined by single
// dots), the domain ASCII letters, digits and hyphens in labels joined by
// dots, with a letter in the last label. Spaces and tabs around an entry
// and empty entries are allowed; an empty text is an empty list.
func PlainAddressList(text string) bool {
	for _, entry := range strings.Split(text, ",") {
		entry = strings.Trim(entry, " \t")
		if entry != "" && !plainAddress(entry) {
			return false
		}
	}
	return true
}

// plainAddress reports whether s is one address of PlainAddressList.
func plainAddress(s string) bool {
	at := strings.LastIndexByte(s, '@')
	if at < 1 || len(s) > 254 {
		return false
	}
	local, domain := s[:at], s[at+1:]
	return len(local) <= 64 && dotAtom(local) && plainDomain(domain)
}

// dotAtom reports whether s is parts of atext joined by single dots.
func dotAtom(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !isAlnum(c) && !strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", rune(c)) {
				return false
			}
		}
	}
	return true
}

// plainDomain reports whether s is labels of ASCII letters, digits and
// inner hyphens joined by dots, whose last label has a letter and is no
// hexadecimal number (0x10), so that the URL parser does not read the
// domain as an IPv4 address in one of its short forms.
func plainDomain(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			if !isAlnum(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	last := strings.ToLower(labels[len(labels)-1])
	hex := strings.HasPrefix(last, "0x") && strings.Trim(last[2:], "0123456789abcdef") == ""
	return !hex && strings.IndexFunc(last, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
