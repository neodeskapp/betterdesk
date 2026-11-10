package serverinfo

import (
	"net/http"
	"net/url"
	"strings"
)

// QueryString returns a parameter of the request's query when it is one
// plain text, and "" otherwise, which is how the routes read "?host=": a
// value that is not a string counts as none. A parameter that is missing,
// given more than once or written with brackets (host[]=, host[x]=),
// which Express reads as a list or an object, is "".
func QueryString(r *http.Request, name string) string {
	value, seen := "", 0
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key := queryDecode(rawKey)
		switch {
		case key == name:
			seen++
			value = queryDecode(rawValue)
		case strings.HasPrefix(key, name+"["):
			return ""
		}
	}
	if seen != 1 {
		return ""
	}
	return value
}

// queryDecode decodes one key or value of a query string: "+" is a space,
// and a text whose escapes do not decode stays as it is.
func queryDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}
