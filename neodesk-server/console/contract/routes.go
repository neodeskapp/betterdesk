package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Route is one route of the console as testdata/routes.json lists it: a
// trimmed copy of an entry of the route inventory.
type Route struct {
	// ID is method and path: "GET /api/devices/:id". When the Node console
	// registers the same method and path again, the later route has a
	// number after it: "GET /api/devices (2)".
	ID string `json:"id"`
	// Method is the HTTP method, or ALL for a route that takes every method.
	Method string `json:"method"`
	// Path is the path as the Node console registered it, in Express
	// syntax: ":id", ":id(\\d+)", ":name?", "*".
	Path string `json:"path"`
	// App is the server the route belongs to. The matrix covers "console".
	App string `json:"app"`
	// Group is the route group, named after the Node route file.
	Group string `json:"group"`
	// CSRF says how the CSRF check applies, as the inventory names it.
	CSRF string `json:"csrf,omitempty"`
	// File is the Node file that registers the route.
	File string `json:"file,omitempty"`
	// ShadowedBy is the ID of an earlier route that answers every request
	// this route could get, so this one is never reached.
	ShadowedBy string `json:"shadowedBy,omitempty"`
	// Unreachable says no request gets to this route: an earlier route or
	// a check in front of it always answers first.
	Unreachable bool `json:"unreachable,omitempty"`
}

// AllMethods is the Method of a route that takes every method.
const AllMethods = "ALL"

// RouteList is the content of testdata/routes.json.
type RouteList struct {
	// Source says what the list was generated from.
	Source string  `json:"source"`
	Routes []Route `json:"routes"`
}

// ConsoleApp is the App of the routes the console port serves.
const ConsoleApp = "console"

// LoadRoutes reads a route list.
func LoadRoutes(file string) (*RouteList, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read the route list: %w", err)
	}
	var list RouteList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("read the route list %s: %w", file, err)
	}
	seen := map[string]bool{}
	for _, route := range list.Routes {
		if route.ID == "" || route.Method == "" || !strings.HasPrefix(route.Path, "/") {
			return nil, fmt.Errorf("route list %s: entry %+v needs id, method and a path starting with /", file, route)
		}
		if seen[route.ID] {
			return nil, fmt.Errorf("route list %s: %q is listed twice", file, route.ID)
		}
		seen[route.ID] = true
	}
	return &list, nil
}

// patternPart is a piece of an Express path: a literal text, or a parameter.
type patternPart struct {
	literal string
	// param is the parameter's name; "*" for a wildcard.
	param string
	// constraint is the regular expression in brackets after the name.
	constraint string
}

var paramName = regexp.MustCompile(`^[A-Za-z0-9_]+`)

// parsePattern splits an Express path into literals and parameters.
func parsePattern(pattern string) []patternPart {
	var parts []patternPart
	literal := func(text string) {
		if n := len(parts); n > 0 && parts[n-1].param == "" {
			parts[n-1].literal += text
			return
		}
		parts = append(parts, patternPart{literal: text})
	}
	for i := 0; i < len(pattern); {
		switch c := pattern[i]; {
		case c == '\\' && i+1 < len(pattern):
			literal(pattern[i+1 : i+2])
			i += 2
		case c == '*':
			parts = append(parts, patternPart{param: "*"})
			i++
		case c == ':' && paramName.MatchString(pattern[i+1:]):
			name := paramName.FindString(pattern[i+1:])
			i += 1 + len(name)
			part := patternPart{param: name}
			if i < len(pattern) && pattern[i] == '(' {
				end := closingBracket(pattern, i)
				part.constraint = pattern[i+1 : end]
				i = end + 1
			}
			if i < len(pattern) && strings.ContainsRune("?+*", rune(pattern[i])) {
				i++
			}
			parts = append(parts, part)
		default:
			literal(pattern[i : i+1])
			i++
		}
	}
	return parts
}

// closingBracket finds the ")" that closes the "(" at open.
func closingBracket(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s) - 1
}

// FillPath puts a value in the place of every parameter of an Express path.
// value gets the parameter's name ("*" for a wildcard) and returns what to
// send; it is escaped for a path.
func FillPath(pattern string, value func(name string) string) string {
	var b strings.Builder
	for _, part := range parsePattern(pattern) {
		if part.param == "" {
			b.WriteString(part.literal)
			continue
		}
		b.WriteString(url.PathEscape(value(part.param)))
	}
	return b.String()
}

// constraintProblem checks a parameter value against the parameter's
// constraint. It returns "" when the value fits or the constraint is not
// one Go can read.
func constraintProblem(part patternPart, value string) string {
	if part.constraint == "" {
		return ""
	}
	re, err := regexp.Compile(`^(?:` + part.constraint + `)$`)
	if err != nil {
		return ""
	}
	if !re.MatchString(value) {
		return fmt.Sprintf("parameter %s must match %s, the placeholder is %q", part.param, part.constraint, value)
	}
	return ""
}

// patternSegments splits an Express path at its slashes, leaving the ones
// inside a constraint alone.
func patternSegments(pattern string) []string {
	if len(pattern) > 1 {
		pattern = strings.TrimSuffix(pattern, "/")
	}
	var segments []string
	depth, start := 0, 1
	for i := 1; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
		case '/':
			if depth == 0 {
				segments = append(segments, pattern[start:i])
				start = i + 1
			}
		}
	}
	if start <= len(pattern) && pattern != "/" {
		segments = append(segments, pattern[start:])
	}
	return segments
}

// casePathVariable is a whole path segment that is a variable: "{ticket}".
var casePathVariable = regexp.MustCompile(`^\{[A-Za-z0-9_]+\}$`)

// matchRoute reports whether a request path (of a case, possibly with
// {variables}) is one the Express path answers. literals counts the path
// segments that matched a literal, so the most specific route can be picked.
func matchRoute(pattern, requestPath string) (literals int, ok bool) {
	want := patternSegments(pattern)
	requestPath = strings.TrimSuffix(requestPath, "/")
	var got []string
	if requestPath != "" {
		got = strings.Split(strings.TrimPrefix(requestPath, "/"), "/")
	}
	for i, segment := range want {
		parts := parsePattern(segment)
		if len(parts) == 1 && parts[0].param == "*" {
			return literals, true
		}
		if i >= len(got) {
			return 0, false
		}
		switch {
		case len(parts) == 1 && parts[0].param == "":
			if !strings.EqualFold(parts[0].literal, got[i]) {
				return 0, false
			}
			literals++
		case casePathVariable.MatchString(got[i]):
			// A variable stands for whatever the parameter takes.
		case len(parts) == 1:
			if constraintProblem(parts[0], got[i]) != "" {
				return 0, false
			}
		default:
			if !segmentRegexp(parts).MatchString(got[i]) {
				return 0, false
			}
		}
	}
	return literals, len(got) == len(want)
}

// segmentRegexp matches one path segment made of literals and parameters.
func segmentRegexp(parts []patternPart) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, part := range parts {
		switch {
		case part.param == "":
			b.WriteString(regexp.QuoteMeta(part.literal))
		case part.param == "*":
			b.WriteString(".*")
		default:
			b.WriteString("[^/]+")
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// maxSlug keeps golden file names short enough for every file system.
const maxSlug = 96

// routeSlug is the golden file name of a matrix route, without ".json":
// "GET /api/devices/:id" gives "GET_api_devices__id".
func routeSlug(method, pattern string) string {
	var b strings.Builder
	for _, part := range parsePattern(strings.TrimPrefix(pattern, "/")) {
		switch {
		case part.param == "*":
			b.WriteString("_any")
		case part.param != "":
			b.WriteString("_" + part.param)
		default:
			b.WriteString(strings.ReplaceAll(part.literal, "/", "_"))
		}
	}
	slug := safeName(b.String())
	if pattern == "/" {
		slug = "root"
	}
	if len(slug) > maxSlug {
		sum := sha256.Sum256([]byte(pattern))
		slug = slug[:maxSlug] + "-" + hex.EncodeToString(sum[:4])
	}
	return method + "_" + slug
}
