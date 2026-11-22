package smtpwire

import "strings"

// serverMessageID reads the Message-ID a server assigned out of its final
// reply. Some servers replace the Message-ID header of a submitted message;
// only the two reply formats known to name the new id are read.
//
//	Outlook.com: 250 2.0.0 OK <id@host.prod.outlook.com> [Hostname=...]
//	Amazon SES:  250 Ok 0100018b5c3c0a4f-...-000000 (only for an SES host)
func serverMessageID(response, host string) string {
	reply := jsTrim(response)
	if id := outlookMessageID(reply); id != "" {
		return id
	}
	const sesPrefix = "250 OK "
	if host == "" || !hasPrefixFold(reply, sesPrefix) {
		return ""
	}
	id := reply[len(sesPrefix):]
	if !isSESID(id) {
		return ""
	}
	region := sesRegion(host)
	if region == "" {
		return ""
	}
	if region == "us-east-1" {
		region = "email"
	}
	return "<" + lowerASCII(id) + "@" + region + ".amazonses.com>"
}

func outlookMessageID(reply string) string {
	const prefix = "250 2.0.0 OK "
	const suffix = ".PROD.OUTLOOK.COM"
	if !hasPrefixFold(reply, prefix) {
		return ""
	}
	rest := reply[len(prefix):]
	if !strings.HasPrefix(rest, "<") {
		return ""
	}
	end := strings.IndexFunc(rest[1:], func(r rune) bool { return r == '<' || r == '>' || isJSSpace(r) })
	if end < 0 || rest[1+end] != '>' {
		return ""
	}
	inner := rest[1 : 1+end]
	if len(inner) <= len(suffix) || !hasPrefixFold(inner[len(inner)-len(suffix):], suffix) {
		return ""
	}
	return rest[:end+2]
}

// isSESID reports whether id is two or more groups of hex digits joined by
// dashes.
func isSESID(id string) bool {
	groups := strings.Split(id, "-")
	if len(groups) < 2 {
		return false
	}
	for _, group := range groups {
		if group == "" {
			return false
		}
		for i := 0; i < len(group); i++ {
			c := group[i]
			if !isDigit(c) && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

// sesRegion finds the AWS region in the name of an SES endpoint such as
// email-smtp.eu-west-1.amazonaws.com, "" for any other host.
func sesRegion(host string) string {
	labels := strings.Split(strings.TrimSuffix(lowerHost(host), "."), ".")
	if len(labels) < 2 {
		return ""
	}
	domain := labels[len(labels)-2] + "." + labels[len(labels)-1]
	if domain != "amazonaws.com" && domain != "awsapps.com" {
		return ""
	}
	for _, label := range labels[:len(labels)-2] {
		if isAWSRegion(label) {
			return label
		}
	}
	return ""
}

// isAWSRegion matches names like us-east-1 and ap-southeast-2: two letters,
// one or more groups of letters, a number.
func isAWSRegion(label string) bool {
	parts := strings.Split(label, "-")
	if len(parts) < 3 || len(parts[0]) != 2 {
		return false
	}
	for i, part := range parts {
		if part == "" {
			return false
		}
		last := i == len(parts)-1
		for j := 0; j < len(part); j++ {
			if last != isDigit(part[j]) || (!last && (part[j] < 'a' || part[j] > 'z')) {
				return false
			}
		}
	}
	return true
}

func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// lowerHost lower-cases a host name as far as it matters for comparing it
// with ASCII names: the ASCII letters and the Kelvin sign, which JavaScript
// lower-cases to "k".
func lowerHost(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		case r == 0x212A:
			return 'k'
		}
		return r
	}, s)
}
