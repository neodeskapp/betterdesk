package message

import (
	"strconv"
	"strings"
)

// This file reads a host name the way Node's url.domainToASCII and
// url.domainToUnicode do. Both run the host parser of a URL like
// "ws://host": a name in square brackets is an IPv6 address, a name whose
// last label is a number is an IPv4 address in any of its spellings, and
// everything else is a domain that goes through IDNA.

// nodeDomainToASCII returns what url.domainToASCII(domain) returns: the
// host in its canonical ASCII form, or "" when it is no valid host.
//
// The caller never passes '/', '\\', '?', '#', '%', a control character or a
// space; the URL parser would cut the host or drop the character there. A
// ':' outside of square brackets would start the port in a URL and makes
// the name no host; here it fails as a character no domain can have.
func nodeDomainToASCII(t *unicodeTables, domain string) string {
	if domain == "" {
		return ""
	}
	if domain[0] == '[' {
		if domain[len(domain)-1] != ']' {
			return ""
		}
		return parseIPv6(domain[1 : len(domain)-1])
	}

	host := asciiLower(domain)
	if hasForbiddenDomainByte(host) || strings.Contains(host, "xn-") {
		// the full way: IDNA, which also checks every "xn--" label
		host = idnaToASCII(t, domain)
		if host == "" || hasForbiddenDomainByte(host) {
			return ""
		}
	}
	if endsInNumber(host) {
		return parseIPv4(host)
	}
	return host
}

// nodeDomainToUnicode returns what url.domainToUnicode(domain) returns: the
// canonical host with every "xn--" label decoded, or "" for no valid host.
func nodeDomainToUnicode(t *unicodeTables, domain string) string {
	return idnaToUnicode(nodeDomainToASCII(t, domain))
}

// hasForbiddenDomainByte reports whether the name holds a byte that a domain
// in a URL cannot have. Every byte outside ASCII counts, so a name that is
// not ASCII always takes the IDNA way.
func hasForbiddenDomainByte(host string) bool {
	for i := 0; i < len(host); i++ {
		c := host[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte("#/:<>?@[\\]^|%", c) >= 0 {
			return true
		}
	}
	return false
}

// endsInNumber reports whether the last label of a lower-case host is a
// number: digits only, or "0x" followed by hex digits or nothing. One dot at
// the end is not part of the label.
func endsInNumber(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	last := host[len(host)-1]
	if !(last >= '0' && last <= '9') && !(last >= 'a' && last <= 'f') && last != 'x' {
		return false
	}
	label := host[strings.LastIndexByte(host, '.')+1:]
	if strings.Trim(label, "0123456789") == "" {
		return true
	}
	return strings.HasPrefix(label, "0x") && strings.Trim(label[2:], "0123456789abcdef") == ""
}

// parseIPv4 reads a lower-case host as an IPv4 address of up to four numbers
// in decimal, octal (leading 0) or hex (leading 0x), where the last number
// fills all the bytes that are left. It returns the address as four decimal
// numbers, or "" when the host is no address. An address that was written
// as four plain decimal numbers comes back as written.
func parseIPv4(host string) string {
	input := strings.TrimSuffix(host, ".")
	original := input
	address := uint64(0)
	decimals := 0
	parts := 0
	for ; parts < 4 && input != ""; parts++ {
		var value uint64
		isHex := len(input) >= 2 && input[0] == '0' && input[1]|0x20 == 'x'
		if isHex && (len(input) == 2 || input[2] == '.') {
			input = input[2:]
		} else {
			digits, base := input, 10
			switch {
			case isHex:
				digits, base = input[2:], 16
			case len(input) >= 2 && input[0] == '0' && input[1] >= '0' && input[1] <= '9':
				digits, base = input[1:], 8
			default:
				decimals++
			}
			end := 0
			for end < len(digits) && digitValue(digits[end]) < base {
				end++
			}
			if end == 0 {
				return ""
			}
			var err error
			if value, err = strconv.ParseUint(digits[:end], base, 32); err != nil {
				return ""
			}
			input = digits[end:]
		}
		if input == "" {
			// the last number fills the rest of the 32 bits
			if value >= 1<<(32-parts*8) {
				return ""
			}
			address = address<<(32-parts*8) | value
			if decimals == 4 {
				return original
			}
			return strconv.Itoa(int(address>>24)) + "." + strconv.Itoa(int(address>>16&0xff)) + "." +
				strconv.Itoa(int(address>>8&0xff)) + "." + strconv.Itoa(int(address&0xff))
		}
		if value > 255 || input[0] != '.' {
			return ""
		}
		address = address<<8 | value
		input = input[1:]
	}
	return ""
}

// digitValue returns the value of a digit in bases up to 16, and 16 for a
// byte that is no digit.
func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 16
}

// parseIPv6 reads the text between the square brackets as an IPv6 address,
// with "::" and an IPv4 tail allowed, and returns its shortest spelling in
// brackets ("[2001:db8::1]"). It returns "" for anything else.
func parseIPv6(input string) string {
	if input == "" {
		return ""
	}
	var address [8]uint16
	piece := 0
	compress := -1
	pointer := 0
	if input[0] == ':' {
		if len(input) == 1 || input[1] != ':' {
			return ""
		}
		pointer = 2
		piece++
		compress = piece
	}
	for pointer < len(input) {
		if piece == 8 {
			return ""
		}
		if input[pointer] == ':' {
			if compress >= 0 {
				return ""
			}
			pointer++
			piece++
			compress = piece
			continue
		}
		value, length := uint16(0), 0
		for length < 4 && pointer < len(input) && digitValue(input[pointer]) < 16 {
			value = value*0x10 + uint16(digitValue(input[pointer]))
			pointer++
			length++
		}
		if pointer < len(input) && input[pointer] == '.' {
			// the rest is an IPv4 address that fills the last two pieces
			if length == 0 || piece > 6 {
				return ""
			}
			pointer -= length
			seen := 0
			for pointer < len(input) {
				if seen > 0 {
					if input[pointer] != '.' || seen >= 4 {
						return ""
					}
					pointer++
				}
				if pointer == len(input) || input[pointer] < '0' || input[pointer] > '9' {
					return ""
				}
				number := -1
				for pointer < len(input) && input[pointer] >= '0' && input[pointer] <= '9' {
					digit := int(input[pointer] - '0')
					switch {
					case number < 0:
						number = digit
					case number == 0:
						return ""
					default:
						number = number*10 + digit
					}
					if number > 255 {
						return ""
					}
					pointer++
				}
				address[piece] = address[piece]*0x100 + uint16(number)
				seen++
				if seen == 2 || seen == 4 {
					piece++
				}
			}
			if seen != 4 {
				return ""
			}
			break
		}
		if pointer < len(input) {
			if input[pointer] != ':' {
				return ""
			}
			pointer++
			if pointer == len(input) {
				return ""
			}
		}
		address[piece] = value
		piece++
	}
	if compress >= 0 {
		// move the pieces after "::" to the end
		swaps := piece - compress
		for piece = 7; piece != 0 && swaps > 0; piece, swaps = piece-1, swaps-1 {
			address[piece], address[compress+swaps-1] = address[compress+swaps-1], address[piece]
		}
	} else if piece != 8 {
		return ""
	}
	return formatIPv6(address)
}

// formatIPv6 writes an address with its first longest run of two or more
// zero pieces as "::".
func formatIPv6(address [8]uint16) string {
	compress, compressLength := 0, 0
	for i := 0; i < 8; i++ {
		if address[i] != 0 {
			continue
		}
		next := i + 1
		for next != 8 && address[next] == 0 {
			next++
		}
		if count := next - i; compressLength < count {
			compressLength = count
			compress = i
			if next == 8 {
				break
			}
			i = next
		}
	}
	if compressLength <= 1 {
		compress, compressLength = 8, 8
	}
	out := []byte{'['}
	for piece := 0; ; {
		if piece == compress {
			out = append(out, ':')
			if piece == 0 {
				out = append(out, ':')
			}
			piece += compressLength
			if piece == 8 {
				break
			}
		}
		out = strconv.AppendUint(out, uint64(address[piece]), 16)
		piece++
		if piece == 8 {
			break
		}
		out = append(out, ':')
	}
	return string(append(out, ']'))
}
