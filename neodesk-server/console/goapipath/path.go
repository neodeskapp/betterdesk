package goapipath

import (
	"errors"
	"regexp"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// maxSegmentLength is the longest value that may become a path segment.
const maxSegmentLength = 256

var absoluteURL = regexp.MustCompile(`(?i)^https?://`)

// AssertSafeGoAPIRelativePath checks a path, with or without a query,
// before it goes to the API server, and returns it unchanged. It keeps a
// call on the API server and out of parent directories. The errors:
//
//	"API path is required"                                          the path is empty
//	"Invalid API path"                                              a backslash or a NUL
//	"Absolute API URLs are not allowed"                             http://, https:// or //
//	"API path must be relative to the configured Go API base URL"   no slash in front
//	"API path must not contain parent-directory segments"           ".." anywhere in the path
//	"Invalid API path segment"                                      a segment that is "."
//	"Invalid query string"                                          a line break or a NUL in the query
func AssertSafeGoAPIRelativePath(urlPath string) (string, error) {
	if urlPath == "" {
		return "", errors.New("API path is required")
	}
	if strings.ContainsAny(urlPath, "\\\x00") {
		return "", errors.New("Invalid API path")
	}
	if absoluteURL.MatchString(urlPath) || strings.HasPrefix(urlPath, "//") {
		return "", errors.New("Absolute API URLs are not allowed")
	}
	pathname, query, _ := strings.Cut(urlPath, "?")
	if !strings.HasPrefix(pathname, "/") {
		return "", errors.New("API path must be relative to the configured Go API base URL")
	}
	if strings.Contains(pathname, "..") {
		return "", errors.New("API path must not contain parent-directory segments")
	}
	for _, segment := range strings.Split(pathname, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("Invalid API path segment")
		}
	}
	if strings.ContainsAny(query, "\r\n\x00") {
		return "", errors.New("Invalid query string")
	}
	return urlPath, nil
}

// AssertSafeAPIID checks a value that is to become one segment of an API
// path and returns it without the white space around it. value is the
// text JavaScript makes of what the route read, String(value || ""): a
// path parameter as it is, a query value through QueryValue.Text. It may
// hold 1 to 256 letters, digits and . _ : @ % + -, must not be "." and
// must not hold ".." anywhere, which the path check of the API client
// refuses. Anything else is the error "Invalid <label>". The label names
// the parameter to the client ("orgId", "deviceId"); "id" is the usual
// one for a route that names nothing.
func AssertSafeAPIID(value, label string) (string, error) {
	v := strings.TrimFunc(value, isJSSpace)
	if v == "" || v == "." || strings.Contains(v, "..") || len(v) > maxSegmentLength || strings.IndexFunc(v, unsafeIDRune) >= 0 {
		return "", errors.New("Invalid " + label)
	}
	return v, nil
}

func unsafeIDRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("._:@%+-", r)
}

// SafeSegment is AssertSafeAPIID with the result escaped for a path, the
// way encodeURIComponent does it: of the characters an id may hold, ":",
// "@", "%" and "+" are written as %3A, %40, %25 and %2B.
func SafeSegment(value, label string) (string, error) {
	id, err := AssertSafeAPIID(value, label)
	if err != nil {
		return "", err
	}
	return goapi.EncodeURIComponent(id), nil
}

// IsInvalid reports whether err is a refusal the routes answer with 400
// and its text: an error whose text starts with "Invalid ". Those are the
// errors of AssertSafeAPIID and SafeSegment, and three of the path check
// ("Invalid API path", "Invalid API path segment", "Invalid query
// string").
func IsInvalid(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "Invalid ")
}

// isJSSpace reports the white space and line ends JavaScript's trim and
// parseInt skip.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}
