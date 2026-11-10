package toolkit

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// maxText is the longest text a text tool takes, in UTF-16 units.
const maxText = 1048576

// text reads body.text for a text tool and answers 400 when it is no
// text or too long.
func (h *handlers) text(w http.ResponseWriter, r *http.Request) (string, bool) {
	text, ok := bodyText(kit.Body(r), "text")
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "Text is required")
		return "", false
	}
	if utf16Len(text) > maxText {
		kit.Fail(w, r, http.StatusBadRequest, "Input too large (max 1 MB)")
		return "", false
	}
	return text, true
}

type hashData struct {
	Algorithm string `json:"algorithm"`
	Hash      string `json:"hash"`
}

// hash answers POST /api/toolkit/hash with the body {"text":"...",
// "algorithm":"md5"|"sha1"|"sha256"|"sha512"}: the digest of the text's
// UTF-8 bytes in hex. Any other algorithm is sha256.
func (h *handlers) hash(w http.ResponseWriter, r *http.Request) {
	text, ok := h.text(w, r)
	if !ok {
		return
	}
	algorithm, _ := kit.Body(r).String("algorithm")
	var digest hash.Hash
	switch algorithm {
	case "md5":
		digest = md5.New()
	case "sha1":
		digest = sha1.New()
	case "sha512":
		digest = sha512.New()
	default:
		algorithm, digest = "sha256", sha256.New()
	}
	digest.Write(utf8Bytes(text))
	kit.OK(w, r, hashData{Algorithm: algorithm, Hash: hex.EncodeToString(digest.Sum(nil))})
}

type passwordData struct {
	Password    string `json:"password"`
	Length      int    `json:"length"`
	Entropy     int    `json:"entropy"`
	CharsetSize int    `json:"charsetSize"`
}

// password answers POST /api/toolkit/password with the body {"length":16,
// "uppercase":true,"lowercase":true,"digits":true,"symbols":false}: a
// random password of 4 to 128 characters from the chosen sets, and its
// entropy in bits. With no set chosen it uses lower-case letters and
// digits.
func (h *handlers) password(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	length := int(body.IntBetween("length", 16, 4, 128))
	if items, isArray := middleware.BodyValue(r).Array(); isArray {
		// A body that is an array has a length of its own
		length = min(max(len(items), 4), 128)
	}
	charset := ""
	if body.Flag("uppercase", true) {
		charset += "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	}
	if body.Flag("lowercase", true) {
		charset += "abcdefghijklmnopqrstuvwxyz"
	}
	if body.Flag("digits", true) {
		charset += "0123456789"
	}
	if body.Flag("symbols", false) {
		charset += "!@#$%^&*()-_=+[]{}|;:,.<>?"
	}
	if charset == "" {
		charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	}

	// Bytes that would favour the first characters of the set are thrown
	// away. The first draw is twice the length, later draws are the length.
	unbiased := 256 - 256%len(charset)
	password := make([]byte, 0, length)
	for draw := length * 2; len(password) < length; draw = length {
		random := make([]byte, draw)
		if _, err := io.ReadFull(h.random, random); err != nil {
			h.log.Error("Password generator error:", err)
			kit.Fail(w, r, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		for _, b := range random {
			if len(password) == length {
				break
			}
			if int(b) < unbiased {
				password = append(password, charset[int(b)%len(charset)])
			}
		}
	}
	entropy := int(math.Floor(float64(length)*math.Log2(float64(len(charset))) + 0.5))
	kit.OK(w, r, passwordData{Password: string(password), Length: length, Entropy: entropy, CharsetSize: len(charset)})
}

type convertData struct {
	Result jsText `json:"result"`
	Mode   any    `json:"mode"`
}

// mode is body.mode || 'encode' as it goes back to the client.
func mode(r *http.Request) (decode bool, back any) {
	value := kit.Body(r).Get("mode")
	if !value.Truthy() {
		return false, "encode"
	}
	text, isText := value.Str()
	return isText && text == "decode", echo(value)
}

// base64 answers POST /api/toolkit/base64 with the body {"text":"...",
// "mode":"decode"}: the text as base64, or with mode decode the text the
// base64 stands for.
func (h *handlers) base64(w http.ResponseWriter, r *http.Request) {
	text, ok := h.text(w, r)
	if !ok {
		return
	}
	decode, back := mode(r)
	var result string
	if decode {
		result = decodeUTF8(decodeBase64(units(text)))
	} else {
		result = base64.StdEncoding.EncodeToString(utf8Bytes(text))
	}
	kit.OK(w, r, convertData{Result: jsText(result), Mode: back})
}

// sextet is the value of a base64 character in either alphabet, -1 for
// any other character.
func sextet(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '+' || c == '-':
		return 62
	case c == '/' || c == '_':
		return 63
	}
	return -1
}

// decodeBase64 is Buffer.from(text, 'base64'), which never refuses: it
// takes both alphabets, skips every character that belongs to neither and
// stops at the first "=". Of a character beyond U+00FF only the low byte
// counts. The result is never longer than the length of the text allows.
func decodeBase64(src []uint16) []byte {
	size := len(src)
	limit := 0
	if size >= 2 {
		if src[size-1] == '=' {
			size--
			if src[size-1] == '=' {
				size--
			}
		}
		if size >= 2 {
			limit = size / 4 * 3
			if rest := size % 4; rest != 0 && !(limit == 0 && rest == 1) {
				limit++
				if rest == 3 {
					limit++
				}
			}
		}
	}
	out := make([]byte, 0, limit)
	bits, held := 0, 0
	for _, unit := range src {
		c := byte(unit)
		if c == '=' {
			break
		}
		v := sextet(c)
		if v < 0 {
			continue
		}
		bits = bits<<6 | v
		held += 6
		if held >= 8 {
			held -= 8
			if len(out) == limit {
				break
			}
			out = append(out, byte(bits>>held))
		}
	}
	return out
}

// urlencode answers POST /api/toolkit/urlencode with the body
// {"text":"...","mode":"decode"}: encodeURIComponent of the text, or with
// mode decode decodeURIComponent. A text neither can handle is answered
// with 400 "URI malformed".
func (h *handlers) urlencode(w http.ResponseWriter, r *http.Request) {
	text, ok := h.text(w, r)
	if !ok {
		return
	}
	decode, back := mode(r)
	var result string
	if decode {
		result, ok = decodeURIComponent(text)
	} else {
		result, ok = encodeURIComponent(text)
	}
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "URI malformed")
		return
	}
	kit.OK(w, r, convertData{Result: jsText(result), Mode: back})
}

const upperHex = "0123456789ABCDEF"

// encodeURIComponent writes every character but letters, digits and
// -_.!~*'() as the %XX of its UTF-8 bytes. ok is false for a text with a
// lone surrogate half.
func encodeURIComponent(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= utf8.RuneSelf && isLoneHalf(s, i):
			return "", false
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0xF])
		}
	}
	return b.String(), true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// decodeURIComponent turns every %XX run into the character its bytes
// are the UTF-8 of. ok is false for a % without two hex digits and for
// bytes that are no UTF-8.
func decodeURIComponent(s string) (string, bool) {
	escaped := func(i int) (byte, bool) {
		if i+2 >= len(s) || s[i] != '%' {
			return 0, false
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		return hi<<4 | lo, ok1 && ok2
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := escaped(i)
		if !ok {
			return "", false
		}
		i += 3
		if first < utf8.RuneSelf {
			b.WriteByte(first)
			continue
		}
		size := 0
		switch {
		case first&0xE0 == 0xC0:
			size = 2
		case first&0xF0 == 0xE0:
			size = 3
		case first&0xF8 == 0xF0:
			size = 4
		default:
			return "", false
		}
		sequence := []byte{first}
		for len(sequence) < size {
			next, ok := escaped(i)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			sequence = append(sequence, next)
			i += 3
		}
		if !utf8.Valid(sequence) {
			return "", false
		}
		b.Write(sequence)
	}
	return b.String(), true
}

type jwtData struct {
	Header  jsJSON     `json:"header"`
	Payload jsJSON     `json:"payload"`
	Expiry  *jwtExpiry `json:"expiry"`
	HasSig  bool       `json:"hasSig"`
}

type jwtExpiry struct {
	Date        string `json:"date"`
	Expired     bool   `json:"expired"`
	SecondsLeft int64  `json:"secondsLeft"`
}

type jwtAnswer struct {
	Success bool    `json:"success"`
	Data    jwtData `json:"data"`
}

// jwtDecode answers POST /api/toolkit/jwt-decode with the body
// {"token":"..."}: header and payload of the token as JSON, when it
// expires, and whether it has a signature part. Nothing is verified.
func (h *handlers) jwtDecode(w http.ResponseWriter, r *http.Request) {
	token, ok := bodyText(kit.Body(r), "token")
	if !ok || token == "" {
		kit.Fail(w, r, http.StatusBadRequest, "Token is required")
		return
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid JWT format")
		return
	}
	refuse := func(message string) {
		kit.Fail(w, r, http.StatusBadRequest, "Failed to decode JWT: "+message)
	}
	var decoded [2]any
	for i := range decoded {
		value, err := parseJSON(decodeUTF8(decodeBase64(units(parts[i]))))
		if err != nil {
			refuse(err.Error())
			return
		}
		decoded[i] = value
	}
	payload := decoded[1]
	if payload == nil {
		refuse("Cannot read properties of null (reading 'exp')")
		return
	}
	data := jwtData{Header: jsJSON{decoded[0]}, Payload: jsJSON{payload}, HasSig: len(parts) == 3}
	if claims, isObject := payload.(*object); isObject {
		if exp, _ := claims.get("exp"); truthy(exp) {
			seconds, ok := toNumber(exp)
			if !ok {
				refuse("Cannot convert object to primitive value")
				return
			}
			// A JavaScript date holds whole milliseconds up to 100 million
			// days from 1970
			ms := seconds * 1000
			if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
				refuse("Invalid time value")
				return
			}
			at := int64(math.Trunc(ms))
			now := h.now().UnixMilli()
			data.Expiry = &jwtExpiry{
				Date:        isoString(at),
				Expired:     now > at,
				SecondsLeft: int64(math.Floor(float64(at-now) / 1000)),
			}
		}
	}
	kit.JSON(w, r, http.StatusOK, jwtAnswer{Success: true, Data: data})
}

// isoString is Date.prototype.toISOString for a time in milliseconds
// since 1970: years outside 0 to 9999 are written with a sign and six
// digits.
func isoString(ms int64) string {
	t := time.UnixMilli(ms).UTC()
	rest := t.Format("-01-02T15:04:05.000Z")
	year := t.Year()
	switch {
	case year >= 0 && year <= 9999:
		return pad(year, 4) + rest
	case year < 0:
		return "-" + pad(-year, 6) + rest
	}
	return "+" + pad(year, 6) + rest
}

func pad(n, width int) string {
	text := ""
	for ; n > 0 || text == ""; n /= 10 {
		text = string(rune('0'+n%10)) + text
	}
	for len(text) < width {
		text = "0" + text
	}
	return text
}
