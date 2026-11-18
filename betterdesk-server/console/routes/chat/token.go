package chat

// bearerToken is the token of an Authorization header: the group of
// /^Bearer\s+(\S+)$/, or "" when it does not match, on the header as Node
// read it, each byte a character (Latin-1). The token is returned in
// UTF-8.
func bearerToken(header string) string {
	text := make([]rune, len(header))
	for i := 0; i < len(header); i++ {
		text[i] = rune(header[i])
	}
	const prefix = "Bearer"
	if len(text) < len(prefix) || string(text[:len(prefix)]) != prefix {
		return ""
	}
	rest := text[len(prefix):]
	spaces := 0
	for spaces < len(rest) && jsSpace(rest[spaces]) {
		spaces++
	}
	token := rest[spaces:]
	if spaces == 0 || len(token) == 0 {
		return ""
	}
	for _, r := range token {
		if jsSpace(r) {
			return ""
		}
	}
	return string(token)
}

// jsSpace is \s of a JavaScript pattern for the characters a Latin-1 text
// can hold.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0:
		return true
	}
	return false
}
