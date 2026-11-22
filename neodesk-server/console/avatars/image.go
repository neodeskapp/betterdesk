package avatars

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// MaxAvatarBytes is the largest picture the console stores: 512 KB.
const MaxAvatarBytes = 512 * 1024

// The picture types the console stores and serves.
const (
	TypePNG  = "image/png"
	TypeJPEG = "image/jpeg"
	TypeWebP = "image/webp"
	TypeGIF  = "image/gif"
)

// allowedType reports whether mime is one of the four picture types.
func allowedType(mime string) bool {
	switch mime {
	case TypePNG, TypeJPEG, TypeWebP, TypeGIF:
		return true
	}
	return false
}

// Error is a refusal of a picture. Code is what callers put into their
// answers: invalid_data_url, unsupported_type, too_large, not_an_image,
// type_mismatch, invalid_url, address_refused, dns_failed, http_status,
// timeout, too_many_redirects, fetch_failed, empty.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Image is a checked picture: its type and its bytes as they are stored.
type Image struct {
	MimeType string
	Data     []byte
}

// SniffImageType tells the type of a picture from its first bytes: the
// PNG signature, the JPEG start marker, "GIF87a" or "GIF89a", or a RIFF
// container of type WEBP. It returns "" for anything else and for less
// than four bytes. Nothing behind the first twelve bytes is looked at:
// the picture is not decoded and its size in pixels is not checked.
func SniffImageType(buf []byte) string {
	if len(buf) < 4 {
		return ""
	}
	if len(buf) >= 8 && string(buf[:8]) == "\x89PNG\r\n\x1a\n" {
		return TypePNG
	}
	if buf[0] == 0xff && buf[1] == 0xd8 && buf[2] == 0xff {
		return TypeJPEG
	}
	if len(buf) >= 6 {
		if head := string(buf[:6]); head == "GIF87a" || head == "GIF89a" {
			return TypeGIF
		}
	}
	if len(buf) >= 12 && string(buf[:4]) == "RIFF" && string(buf[8:12]) == "WEBP" {
		return TypeWebP
	}
	return ""
}

// normalizeMime returns the media type of a Content-Type value in lower
// case without its parameters, with the two other names of JPEG mapped to
// image/jpeg.
func normalizeMime(value string) string {
	m, _, _ := strings.Cut(value, ";")
	m = asciiLower(jsTrim(m))
	if m == "image/jpg" || m == "image/pjpeg" {
		return TypeJPEG
	}
	return m
}

// asciiLower lowers the letters A to Z. No letter outside ASCII becomes
// one of the letters in a picture type's name when JavaScript lowers it,
// so such letters stay as they are and the type stays unknown.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// ImageVersion is the short name of a picture's bytes: the first 16 hex
// digits of their SHA-256. It is the ?v= of the picture's address and its
// ETag.
func ImageVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// ParseDataURL decodes and checks a data:image URL. The text must be
// "data:image/<type>;base64," and base64 text (either alphabet, up to two
// "=" at the end); the declared type must be PNG, JPEG, WebP or GIF and
// match the first bytes. maxBytes is the size limit, MaxAvatarBytes when
// it is zero or less.
func ParseDataURL(value string, maxBytes int) (Image, error) {
	if maxBytes <= 0 {
		maxBytes = MaxAvatarBytes
	}
	declared, payload, ok := splitDataURL(jsTrim(value))
	if !ok {
		return Image{}, refuse("invalid_data_url", "Not a base64 data:image URL")
	}
	declared = normalizeMime(declared)
	if !allowedType(declared) {
		return Image{}, refuse("unsupported_type", "Type "+declared+" is not allowed")
	}
	// Refused before decoding when the text alone is clearly too long
	if len(payload)*3/4 > maxBytes+3 {
		return Image{}, refuse("too_large", "Picture is too large")
	}
	data := decodeBase64(payload)
	if len(data) == 0 {
		return Image{}, refuse("invalid_data_url", "Empty picture")
	}
	if len(data) > maxBytes {
		return Image{}, refuse("too_large", "Picture is too large")
	}
	sniffed := SniffImageType(data)
	if sniffed == "" {
		return Image{}, refuse("not_an_image", "The data is not a PNG, JPEG, WebP or GIF image")
	}
	if sniffed != declared {
		return Image{}, refuse("type_mismatch", "Declared "+declared+" but the data is "+sniffed)
	}
	return Image{MimeType: sniffed, Data: data}, nil
}

// splitDataURL takes "data:image/<type>;base64,<text>" apart. The words
// are matched in any letter case; the type is letters, digits and ".+-";
// the text is at least one base64 character of either alphabet followed
// by at most two "=".
func splitDataURL(s string) (mime, payload string, ok bool) {
	const scheme, marker = "data:", ";base64,"
	if !hasPrefixFold(s, scheme) {
		return "", "", false
	}
	rest := s[len(scheme):]
	const family = "image/"
	if !hasPrefixFold(rest, family) {
		return "", "", false
	}
	end := len(family)
	for end < len(rest) && isTypeChar(rest[end]) {
		end++
	}
	if end == len(family) {
		return "", "", false
	}
	mime, rest = rest[:end], rest[end:]
	if !hasPrefixFold(rest, marker) {
		return "", "", false
	}
	payload = rest[len(marker):]
	body := strings.TrimRight(payload, "=")
	if body == "" || len(payload)-len(body) > 2 {
		return "", "", false
	}
	for i := 0; i < len(body); i++ {
		if base64Value(body[i]) < 0 {
			return "", "", false
		}
	}
	return mime, payload, true
}

func isTypeChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '+' || c == '-'
}

// base64Value is the six bits a character stands for in the standard or
// the URL alphabet, -1 for any other character.
func base64Value(c byte) int {
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

// decodeBase64 decodes base64 text the forgiving way: both alphabets may
// be mixed, padding is optional, and the bits left over in a last group
// are dropped. A lone character at the end gives no byte. The text ends
// at the first "=".
func decodeBase64(text string) []byte {
	out := make([]byte, 0, len(text)*3/4)
	var acc, bits uint
	for i := 0; i < len(text); i++ {
		v := base64Value(text[i])
		if v < 0 {
			break
		}
		acc = acc<<6 | uint(v)
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	return out
}

// jsTrim removes what JavaScript's String.prototype.trim removes: white
// space, line ends and the byte order mark.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// sliceUnits returns the first n UTF-16 code units of s, as JavaScript's
// slice(0, n) does. A character outside the Basic Multilingual Plane that
// the cut falls into leaves its first half, which is stored as U+FFFD.
func sliceUnits(s string, n int) string {
	units := 0
	for i, r := range s {
		size := 1
		if r > 0xffff {
			size = 2
		}
		if units+size > n {
			if units < n {
				return s[:i] + string(unicode.ReplacementChar)
			}
			return s[:i]
		}
		units += size
	}
	return s
}
