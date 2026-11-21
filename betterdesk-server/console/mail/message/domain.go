package message

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Limits of the DNS: a label has at most 63 characters and a name 253.
const (
	maxDomainLength = 253
	maxLabelLength  = 63
)

// maxMappedLength is the longest domain, in characters, that is mapped and
// encoded. The URL host parser takes longer domains and labels than the
// DNS does, and nodemailer encoded them; the limit keeps that for every
// domain a mail could name and bounds the work for one.
const maxMappedLength = 1024

// errPunycode is the failure of the fallback codec. The caller keeps the
// domain as it was written then.
var errPunycode = errors.New("punycode: invalid input")

// normalizeDomain returns the form of a lower-case domain that goes into
// the address: the ASCII form ("xn--..." labels), or with toUnicode the
// Unicode form, which is used next to a local part that is not ASCII.
//
// A domain of more than maxMappedLength characters comes back as it is,
// before anything maps it: the mapping sorts combining marks, and Punycode
// encodes, in time by the square of the length. A domain the URL host
// parser accepts comes back in its canonical form, IP addresses included.
// Everything else goes through a plain Punycode codec that maps and checks
// nothing, unless it is too long to be a domain; that codec can fail.
func normalizeDomain(t *unicodeTables, domain string, toUnicode bool) (string, error) {
	if utf8.RuneCountInString(domain) > maxMappedLength {
		return domain, nil
	}
	if !hasURLUnsafe(domain) {
		mapped := nodeDomainToASCII(t, domain)
		if mapped != "" && toUnicode {
			mapped = nodeDomainToUnicode(t, mapped)
		}
		if mapped != "" {
			return mapped, nil
		}
	}
	if exceedsDomainLimits(domain) {
		return domain, nil
	}
	if toUnicode {
		return fallbackToUnicode(t, domain)
	}
	return fallbackToASCII(domain)
}

// hasURLUnsafe reports whether the domain holds a character the URL host
// parser would cut the name at, drop or decode. Such a domain must not
// reach that parser: "evil.example/mail.corp.example" would come back as
// "evil.example".
func hasURLUnsafe(domain string) bool {
	for i := 0; i < len(domain); i++ {
		c := domain[i]
		if c <= ' ' || c == 0x7f || c == '/' || c == '\\' || c == '?' || c == '#' || c == '%' {
			return true
		}
	}
	return false
}

// isLabelSeparator reports whether a character separates labels for the
// fallback codec: the full stop and its three wide forms.
func isLabelSeparator(r rune) bool {
	return r == '.' || r == 0x3002 || r == 0xff0e || r == 0xff61
}

// exceedsDomainLimits reports whether a label or the whole domain is longer
// than the DNS allows, counted in characters.
func exceedsDomainLimits(domain string) bool {
	total, label := 0, 0
	for _, r := range domain {
		total++
		if isLabelSeparator(r) {
			label = 0
		} else if label++; label > maxLabelLength {
			return true
		}
	}
	return total > maxDomainLength
}

// fallbackLabels applies convert to every label of a domain and joins the
// results with '.'.
//
// When the domain holds an '@' (a domain literal such as "[a@b]" can), the
// part before the first one is kept as it is, the part up to the second one
// is converted, and the rest is lost.
func fallbackLabels(domain string, convert func(label string) (string, error)) (string, error) {
	prefix := ""
	if parts := strings.Split(domain, "@"); len(parts) > 1 {
		prefix = parts[0] + "@"
		domain = parts[1]
	}
	labels := splitFunc(domain, isLabelSeparator)
	for i, label := range labels {
		converted, err := convert(label)
		if err != nil {
			return "", err
		}
		labels[i] = converted
	}
	return prefix + strings.Join(labels, "."), nil
}

// splitFunc splits s at every character that isSeparator accepts and keeps
// empty parts.
func splitFunc(s string, isSeparator func(rune) bool) []string {
	var parts []string
	start := 0
	for i, r := range s {
		if isSeparator(r) {
			parts = append(parts, s[start:i])
			start = i + len(string(r))
		}
	}
	return append(parts, s[start:])
}

// fallbackToASCII encodes every label that is not ASCII as "xn--" plus its
// Punycode form, without mapping or checking it.
func fallbackToASCII(domain string) (string, error) {
	return fallbackLabels(domain, func(label string) (string, error) {
		if isASCII(label) {
			return label, nil
		}
		encoded, err := fallbackEncode([]rune(label))
		return "xn--" + encoded, err
	})
}

// fallbackToUnicode decodes every label that starts with "xn--", without
// checking what it decodes to.
func fallbackToUnicode(t *unicodeTables, domain string) (string, error) {
	return fallbackLabels(domain, func(label string) (string, error) {
		if !strings.HasPrefix(label, "xn--") {
			return label, nil
		}
		return fallbackDecode(jsToLower(t, label[4:]))
	})
}

func fallbackAdapt(delta, count int64, first bool) int64 {
	if first {
		delta /= punyDamp
	} else {
		delta >>= 1
	}
	delta += delta / count
	k := int64(0)
	for delta > (punyBase-punyTMin)*punyTMax>>1 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}

func fallbackThreshold(k, bias int64) int64 {
	switch {
	case k <= bias:
		return punyTMin
	case k >= bias+punyTMax:
		return punyTMax
	}
	return k - bias
}

// fallbackEncode returns the Punycode form of a label.
func fallbackEncode(label []rune) (string, error) {
	digitChar := func(digit int64) byte {
		if digit < 26 {
			return byte(digit + 'a')
		}
		return byte(digit + 22)
	}
	var out []byte
	for _, r := range label {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	n, delta, bias := int64(punyInitialN), int64(0), int64(punyInitialBias)
	for handled < len(label) {
		m := int64(maxInt32)
		for _, r := range label {
			if int64(r) >= n && int64(r) < m {
				m = int64(r)
			}
		}
		if m-n > (maxInt32-delta)/int64(handled+1) {
			return "", errPunycode
		}
		delta += (m - n) * int64(handled+1)
		n = m
		for _, r := range label {
			if int64(r) < n {
				if delta++; delta > maxInt32 {
					return "", errPunycode
				}
			}
			if int64(r) == n {
				q := delta
				for k := int64(punyBase); ; k += punyBase {
					threshold := fallbackThreshold(k, bias)
					if q < threshold {
						break
					}
					out = append(out, digitChar(threshold+(q-threshold)%(punyBase-threshold)))
					q = (q - threshold) / (punyBase - threshold)
				}
				out = append(out, digitChar(q))
				bias = fallbackAdapt(delta, int64(handled+1), handled == basic)
				delta = 0
				handled++
			}
		}
		delta++
		n++
	}
	return string(out), nil
}

// fallbackDecode decodes the part of a label after "xn--". Letters of
// either case count as digits. A value that is half of a surrogate pair
// joins a matching neighbour and is U+FFFD otherwise.
func fallbackDecode(input string) (string, error) {
	if !isASCII(input) {
		return "", errPunycode
	}
	var output []int64
	basic := max(strings.LastIndexByte(input, '-'), 0)
	for j := 0; j < basic; j++ {
		output = append(output, int64(input[j]))
	}
	index := 0
	if basic > 0 {
		index = basic + 1
	}
	n, i, bias := int64(punyInitialN), int64(0), int64(punyInitialBias)
	for index < len(input) {
		oldI := i
		w := int64(1)
		for k := int64(punyBase); ; k += punyBase {
			if index >= len(input) {
				return "", errPunycode
			}
			var digit int64
			switch c := input[index]; {
			case c >= '0' && c <= '9':
				digit = int64(c-'0') + 26
			case c >= 'A' && c <= 'Z':
				digit = int64(c - 'A')
			case c >= 'a' && c <= 'z':
				digit = int64(c - 'a')
			default:
				return "", errPunycode
			}
			index++
			if digit > (maxInt32-i)/w {
				return "", errPunycode
			}
			i += digit * w
			threshold := fallbackThreshold(k, bias)
			if digit < threshold {
				break
			}
			if w > maxInt32/(punyBase-threshold) {
				return "", errPunycode
			}
			w *= punyBase - threshold
		}
		count := int64(len(output)) + 1
		bias = fallbackAdapt(i-oldI, count, oldI == 0)
		if i/count > maxInt32-n {
			return "", errPunycode
		}
		n += i / count
		i %= count
		output = append(output, 0)
		copy(output[i+1:], output[i:])
		output[i] = n
		i++
	}
	units := make([]uint16, 0, len(output))
	for _, c := range output {
		switch {
		case c > 0x10ffff:
			return "", errPunycode
		case c >= 0x10000:
			c -= 0x10000
			units = append(units, uint16(0xd800+(c>>10)), uint16(0xdc00+(c&0x3ff)))
		default:
			units = append(units, uint16(c))
		}
	}
	return unitsString(units), nil
}
