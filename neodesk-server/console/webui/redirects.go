package webui

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// redirect is one old address of the console and where it leads now. The
// build writes the list as redirects.json from the web app's table
// (web-ui/src/lib/redirects.ts), which the app follows for its own links;
// the server follows it for an address it is asked for first: a bookmark,
// a link in a mail or one typed in.
type redirect struct {
	// From is the old path, in any letter case.
	From string `json:"from"`
	// Tab, when set, is the ?tab= value the old address needs (in any
	// letter case); the new address leaves the old tab out.
	Tab *string `json:"tab"`
	// Query lists other parameters the old address needs, each with its
	// value (in any letter case); the new address leaves them out.
	Query map[string]string `json:"query"`
	// Hash, when set, lists the #ids the old address needs. A browser never
	// sends the #id, so the app alone follows such a rule; the server skips
	// it.
	Hash []string `json:"hash"`
	// To is the new path, with its own query and #id when it has them.
	To string `json:"to"`
}

// loadRedirects reads the build's redirects.json. A missing or unusable
// file is no redirects.
func (h *Host) loadRedirects() []redirect {
	raw, err := h.files.ReadFile(filepath.Join(h.dir, "redirects.json"))
	if err != nil {
		return nil
	}
	var list []redirect
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	return list
}

// redirectFor returns the new address (without the base) of an old one:
// the path without the base and the raw query. The parameters of the old
// address go along, after the new address's own ones; a #id of the new
// address replaces the one the browser had, otherwise the browser keeps
// its own.
func redirectFor(list []redirect, sub, query string) (string, bool) {
	tab, hasTab := queryParam(query, "tab")
	for _, r := range list {
		if len(r.Hash) > 0 || !asciiEqualFold(sub, r.From) {
			continue
		}
		if r.Tab != nil && (!hasTab || !asciiEqualFold(tab, *r.Tab)) {
			continue
		}
		if !hasParams(query, r.Query) {
			continue
		}
		to, hash, _ := strings.Cut(r.To, "#")
		path, toQuery, _ := strings.Cut(to, "?")
		var parts []string
		own := map[string]bool{}
		for _, part := range splitQuery(toQuery) {
			parts = append(parts, part)
			own[queryKey(part)] = true
		}
		for _, part := range splitQuery(query) {
			key := queryKey(part)
			if _, needed := r.Query[key]; own[key] || needed || (r.Tab != nil && key == "tab") {
				continue
			}
			parts = append(parts, part)
		}
		target := path
		if len(parts) > 0 {
			target += "?" + strings.Join(parts, "&")
		}
		if hash != "" {
			target += "#" + hash
		}
		return target, true
	}
	return "", false
}

// hasParams reports whether a raw query has every parameter of a rule with
// its value, in any letter case.
func hasParams(query string, want map[string]string) bool {
	for name, value := range want {
		got, ok := queryParam(query, name)
		if !ok || !asciiEqualFold(got, value) {
			return false
		}
	}
	return true
}

func splitQuery(query string) []string {
	var out []string
	for _, part := range strings.Split(query, "&") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// queryKey is the decoded name of one name=value part of a query.
func queryKey(part string) string {
	key, _, _ := strings.Cut(part, "=")
	if decoded, err := url.QueryUnescape(key); err == nil {
		return decoded
	}
	return key
}

// queryParam is the first value of a parameter, decoded.
func queryParam(query, name string) (string, bool) {
	for _, part := range splitQuery(query) {
		if queryKey(part) != name {
			continue
		}
		_, value, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(value); err == nil {
			return decoded, true
		}
		return value, true
	}
	return "", false
}
