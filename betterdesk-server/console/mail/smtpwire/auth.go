package smtpwire

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The three login mechanisms and what each sends.

// plainAuthCommand is the whole AUTH PLAIN login: an empty authorization
// identity, the user and the password.
func plainAuthCommand(user, pass string) string {
	return "AUTH PLAIN " + encodeBase64("\x00"+user+"\x00"+pass)
}

func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// cramChallenge takes the challenge out of the reply to AUTH CRAM-MD5:
// "334", white space, then at least one character and no line break up to
// the end of the reply.
func cramChallenge(reply string) (string, bool) {
	if !strings.HasPrefix(reply, "334") {
		return "", false
	}
	rest := reply[3:]
	blanks, lastBlank := 0, 0
	for blanks < len(rest) {
		r, size := utf8.DecodeRuneInString(rest[blanks:])
		if !isJSSpace(r) {
			break
		}
		lastBlank = size
		blanks += size
	}
	if blanks == 0 {
		return "", false
	}
	challenge := rest[blanks:]
	if challenge == "" {
		// nothing but white space: its last character is the challenge
		// when another one is left to separate it from the code
		if blanks == lastBlank {
			return "", false
		}
		challenge = rest[blanks-lastBlank:]
	}
	if strings.IndexFunc(challenge, isJSLineEnd) >= 0 {
		return "", false
	}
	return challenge, true
}

// cramResponse answers a CRAM-MD5 challenge: the user, a space and the
// HMAC-MD5 of the decoded challenge keyed with the password, in base64.
// The decoded challenge is used as 7-bit text.
func cramResponse(user, pass, challenge string) string {
	decoded := decodeBase64Lenient(challenge)
	for i := range decoded {
		decoded[i] &= 0x7F
	}
	mac := hmac.New(md5.New, []byte(pass))
	mac.Write(decoded)
	return encodeBase64(user + " " + hex.EncodeToString(mac.Sum(nil)))
}

// decodeBase64Lenient decodes base64 the way Node's Buffer does: both
// alphabets, characters outside them skipped, "=" ends the input, missing
// padding is fine. A character above U+00FF counts as its low byte.
func decodeBase64Lenient(s string) []byte {
	out := make([]byte, 0, len(s)/4*3+3)
	var group [4]byte
	filled := 0
	for _, unit := range utf16.Encode([]rune(s)) {
		b := byte(unit)
		if b == '=' {
			break
		}
		v := base64Value(b)
		if v < 0 {
			continue
		}
		group[filled] = byte(v)
		filled++
		switch filled {
		case 2:
			out = append(out, group[0]<<2|group[1]>>4)
		case 3:
			out = append(out, group[1]<<4|group[2]>>2)
		case 4:
			out = append(out, group[2]<<6|group[3])
			filled = 0
		}
	}
	return out
}

func base64Value(b byte) int {
	switch {
	case b >= 'A' && b <= 'Z':
		return int(b - 'A')
	case b >= 'a' && b <= 'z':
		return int(b-'a') + 26
	case b >= '0' && b <= '9':
		return int(b-'0') + 52
	case b == '+' || b == '-':
		return 62
	case b == '/' || b == '_':
		return 63
	}
	return -1
}
