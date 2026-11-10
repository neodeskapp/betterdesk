package authn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	xbcrypt "golang.org/x/crypto/bcrypt"
	xpbkdf2 "golang.org/x/crypto/pbkdf2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/unitronix/betterdesk-server/auth"
)

// The fuzz tests check the parsers of stored and outside text against a
// second, independent reading of the same rule.
//
//	go test ./console/authn/ -run '^$' -fuzz FuzzStoredHash -fuzztime 10m

var (
	strictBcrypt = regexp.MustCompile(`^\$2([ab]?)\$(\d\d)\$([./A-Za-z0-9]{53})$`)
	strictModern = regexp.MustCompile(`^pbkdf2-sha256\$([^$]*)\$([^$]*)\$([^$]*)$`)
	strictLegacy = regexp.MustCompile(`^([0-9a-fA-F]{32}):([0-9a-fA-F]{64})$`)
	leadingInt   = regexp.MustCompile(`^[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*([+-]?\d+)`)
	leadingHex   = regexp.MustCompile(`^(?:[0-9a-fA-F]{2})*`)
)

// independentVerify decides with other libraries whether stored is a hash
// of password: bcrypt from golang.org/x/crypto, fed the bytes the C code
// feeds Blowfish, and PBKDF2 from golang.org/x/crypto.
func independentVerify(password, stored string) bool {
	if strings.HasPrefix(stored, "pbkdf2-sha256$") {
		m := strictModern.FindStringSubmatch(stored)
		number := leadingInt.FindStringSubmatch(stored[len("pbkdf2-sha256$"):])
		if m == nil || number == nil || !strings.HasPrefix(m[1], number[0]) {
			return false
		}
		iterations, err := strconv.Atoi(number[1])
		if err != nil || iterations <= 0 || iterations > 10_000_000 {
			return false
		}
		salt, _ := hex.DecodeString(leadingHex.FindString(m[2]))
		want, _ := hex.DecodeString(leadingHex.FindString(m[3]))
		if len(salt) == 0 || len(want) == 0 {
			return false
		}
		return string(xpbkdf2.Key([]byte(password), salt, iterations, len(want), sha256.New)) == string(want)
	}
	if m := strictLegacy.FindStringSubmatch(stored); m != nil {
		salt, _ := hex.DecodeString(m[1])
		want, _ := hex.DecodeString(m[2])
		return string(xpbkdf2.Key([]byte(password), salt, 100_000, 32, sha256.New)) == string(want)
	}
	text, _, _ := strings.Cut(stored, "\x00")
	m := strictBcrypt.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	if cost, _ := strconv.Atoi(m[2]); cost < 4 || cost > 30 {
		return false
	}
	// The key the C code reads, spread over the 72 bytes Blowfish takes.
	key := append([]byte(password), 0)
	switch m[1] {
	case "b":
		key = key[:min(len(password), 72)+1]
	case "a":
		key = key[:max((len(password)+1)&0xff, 1)]
	default:
		key = key[:max(len(password)&0xff, 1)]
	}
	spread := make([]byte, 72)
	for i := range spread {
		spread[i] = key[i%len(key)]
	}
	// The library compares the stored salt letters with themselves, so the
	// stray bits of the last salt letter have to be ruled out here.
	if strings.IndexByte(".Oeu", m[3][21]) < 0 {
		return false
	}
	return xbcrypt.CompareHashAndPassword([]byte(text), spread) == nil
}

// tooCostly keeps hashes out of the fuzz run that take long to compute.
func tooCostly(stored string) bool {
	if p, ok := parseBcrypt(stored); ok && p.cost > 5 {
		return true
	}
	if strings.HasPrefix(stored, "pbkdf2-sha256$") {
		parts := strings.Split(stored, "$")
		if len(parts) == 4 {
			if n, ok := parseInt(parts[1]); ok && n > 2000 {
				return true
			}
			return len(parts[3]) > 512
		}
	}
	return false
}

func FuzzStoredHash(f *testing.F) {
	s := pureService()
	ctx := context.Background()
	var vectors []struct {
		Group    string `json:"group"`
		Password string `json:"password"`
		Hash     string `json:"hash"`
	}
	loadSeeds(f, "hashes.json", &vectors)
	seen := map[string]int{}
	for _, v := range vectors {
		if seen[v.Group] < 25 && !tooCostly(v.Hash) && v.Group != "pbkdf2-legacy" {
			seen[v.Group]++
			f.Add(v.Hash, v.Password)
		}
	}
	f.Add("", "")
	f.Add("$2b$04$", "x")
	f.Add("pbkdf2-sha256$1$00$00", "")
	f.Add("00000000000000000000000000000000:0000000000000000000000000000000000000000000000000000000000000000", "pw")

	f.Fuzz(func(t *testing.T, stored, password string) {
		if tooCostly(stored) || len(password) > 4096 {
			t.Skip()
		}
		check, err := s.VerifyPasswordEx(ctx, password, stored)
		if err != nil {
			t.Fatalf("VerifyPasswordEx(%q, %q): %v", password, stored, err)
		}
		if check.NeedsMigration != isPBKDF2Hash(stored) {
			t.Fatalf("NeedsMigration = %v for %q", check.NeedsMigration, stored)
		}
		if want := independentVerify(toWellFormed(password), stored); check.Valid != want {
			t.Fatalf("VerifyPasswordEx(%q, %q) = %v, the second reading says %v", password, stored, check.Valid, want)
		}
	})
}

func loadSeeds(f *testing.F, name string, into any) {
	f.Helper()
	raw, err := readVectorFile(name)
	if err != nil {
		f.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		f.Fatal(err)
	}
}

func FuzzTOTPCode(f *testing.F) {
	secrets := []string{"GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "K5MQOUIXDV6D4BLCPFKQ6YRRJ4LQS43T", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "77777777777777777777777777777777"}
	for _, token := range []string{"287082", "000000", "", " 287082", "287082 ", "28708", "2870820", "abcdef", "-87082", "２８７０８２", "287082\n", "287 082"} {
		f.Add(token, uint8(0), int64(59000), "")
	}
	f.Add("123456", uint8(1), int64(1794287700123), "GEZD GNBV")
	f.Add("123456", uint8(2), int64(0), "é")

	f.Fuzz(func(t *testing.T, token string, pick uint8, at int64, oddSecret string) {
		if at < 0 || at > 253402300799000 {
			t.Skip()
		}
		when := time.UnixMilli(at)
		// Any stored secret: an answer or an error, never a panic, and a
		// token that passes is six ASCII digits.
		if ok, err := totpVerify(token, oddSecret, when); ok && (err != nil || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(token)) {
			t.Fatalf("totpVerify(%q, %q) accepts: %v", token, oddSecret, err)
		}
		// A secret as the console makes them: the code is the one of
		// RFC 6238 for this step and no other.
		secret := secrets[int(pick)%len(secrets)]
		want, err := auth.ComputeTOTP(secret, when)
		if err != nil {
			t.Fatal(err)
		}
		got, err := totpVerify(token, secret, when)
		if err != nil || got != (token == want) {
			t.Fatalf("totpVerify(%q, %s, %d) = %v, %v; the code of the step is %s", token, secret, at, got, err, want)
		}
	})
}

func FuzzRecoveryCode(f *testing.F) {
	for _, stored := range []string{`["3FA9-0C7E","AB12-CD34"]`, `[]`, `not json`, `{}`, `"x"`, `null`, `[1,"A"]`, `["A",1]`, ` ["a" , "B"] `, `["ß"]`, `[["A"]]`, `["A","A"]`} {
		for _, code := range []string{"3FA9-0C7E", "3fa9-0c7e", " AB12-CD34\n", "A", "a", "", "SS", "1"} {
			f.Add(stored, code)
		}
	}
	f.Fuzz(func(t *testing.T, stored, code string) {
		if len(stored) > 1<<16 {
			t.Skip()
		}
		left, matched, err := takeRecoveryCode(stored, code)
		if !matched {
			if left != "" {
				t.Fatalf("no match but a new list %q", left)
			}
			return
		}
		if err != nil {
			t.Fatalf("a match with an error: %v", err)
		}
		// A match means: the stored text is a list, one of its texts is the
		// code without regard to case and outer white space, and the new
		// list is the old one without exactly that entry.
		var before, after []json.RawMessage
		if json.Unmarshal([]byte(stored), &before) != nil || json.Unmarshal([]byte(left), &after) != nil {
			t.Fatalf("lists do not parse: %q -> %q", stored, left)
		}
		if len(after) != len(before)-1 {
			t.Fatalf("%d codes became %d: %q -> %q", len(before), len(after), stored, left)
		}
		gone := -1
		for i := range before {
			if i >= len(after) || !sameJSONValue(before[i], after[i]) {
				gone = i
				break
			}
		}
		for i := gone + 1; i < len(before); i++ {
			if !sameJSONValue(before[i], after[i-1]) {
				t.Fatalf("more than one entry changed: %q -> %q", stored, left)
			}
		}
		var taken string
		if err := json.Unmarshal(before[gone], &taken); err != nil {
			t.Fatalf("the entry taken is not text: %s", before[gone])
		}
		if upper := cases.Upper(language.Und); upper.String(taken) != upper.String(jsTrim(toWellFormed(code))) {
			t.Fatalf("the code %q took the entry %q", code, taken)
		}
		for i := 0; i < gone; i++ {
			var earlier string
			if json.Unmarshal(before[i], &earlier) != nil {
				t.Fatalf("an entry before the match is not text: %s", before[i])
			}
		}
	})
}

func sameJSONValue(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return toJSON(x) == toJSON(y)
}

// plainOrigin is scheme and host with nothing after it: no path, no user,
// no white space.
var plainOrigin = regexp.MustCompile(`^https?://[^/?#@\x{5c}\s]+$`)

var plainHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

func FuzzOIDCRedirect(f *testing.F) {
	var vectors struct {
		Parsed []struct {
			Raw string `json:"raw"`
		} `json:"parsed"`
	}
	loadSeeds(f, "oidc.json", &vectors)
	for i, v := range vectors.Parsed {
		if i%12 == 0 {
			f.Add(v.Raw, "user:pw", "example.org", "5443")
		}
	}
	f.Add("https://good.example@evil.example/api/auth/oidc/callback", "good.example", "evil.example", "")
	f.Add("https://host/api/auth/oidc/callback", "a@b", "host.internal", "443")

	f.Fuzz(func(t *testing.T, raw, userinfo, host, port string) {
		if len(raw) > 2048 {
			t.Skip()
		}
		// Whatever the text: an accepted Redirect URL is an http or https
		// URL to one of the two callback paths, and reading its own origin
		// and path again gives the same.
		got := ParseOIDCRedirectURL(raw)
		if got.OK != IsValidOIDCRedirectURL(raw) {
			t.Fatalf("%q: OK and IsValid disagree", raw)
		}
		if got.OK {
			if got.Reason != "" || got.Pathname != oidcCallbackPath && got.Pathname != oidcShortCallbackPath {
				t.Fatalf("%q accepted as %+v", raw, got)
			}
			if !plainOrigin.MatchString(got.Origin) {
				t.Fatalf("%q: origin %q is no plain http origin", raw, got.Origin)
			}
			again := ParseOIDCRedirectURL(got.Origin + got.Pathname)
			if !again.OK || again.Origin != got.Origin || again.Pathname != got.Pathname {
				t.Fatalf("%q: %q read again gives %+v", raw, got.Origin+got.Pathname, again)
			}
		} else if got.Reason == "" || got.Pathname != "" || got.Origin != "" {
			t.Fatalf("%q refused as %+v", raw, got)
		}
		if suggestion := SuggestOIDCRedirectURL(raw); suggestion != "" {
			if !strings.HasSuffix(suggestion, oidcCallbackPath) {
				t.Fatalf("%q: suggestion %q", raw, suggestion)
			}
			read := ParseOIDCRedirectURL(suggestion)
			if !read.OK || read.Origin+read.Pathname != suggestion {
				t.Fatalf("%q: suggestion %q is not a Redirect URL the settings accept: %+v", raw, suggestion, read)
			}
		}

		// A URL put together from parts: whatever stands before the "@",
		// the origin is the host after it. A name before the "@" that looks
		// like a host must never become the host.
		if !plainHost.MatchString(host) || len(host) > 200 || strings.ContainsAny(userinfo, "/\\?#\t\n\r") || !utf8.ValidString(userinfo) {
			return
		}
		last := host[strings.LastIndexByte(host, '.')+1:]
		if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") || strings.Contains(host, "xn--") {
			return // a number at the end makes it an address, and "xn--" has rules of its own
		}
		authority := userinfo + "@" + host
		wantOrigin := "https://" + host
		if n, err := strconv.Atoi(port); err == nil && n >= 0 && n <= 65535 && strings.Trim(port, "0123456789") == "" {
			authority += ":" + port
			if n != 443 {
				wantOrigin += ":" + strconv.Itoa(n)
			}
		}
		built := "https://" + authority + oidcCallbackPath
		if strings.TrimFunc(built, func(r rune) bool { return r <= 0x20 }) != built {
			return
		}
		read := ParseOIDCRedirectURL(built)
		if !read.OK || read.Origin != wantOrigin {
			t.Fatalf("%q: read as %+v, the host after the last @ is %q", built, read, wantOrigin)
		}
		if got := SuggestOIDCRedirectURL("https://" + authority); got != wantOrigin+oidcCallbackPath {
			t.Fatalf("suggestion for %q: %q, want %q", "https://"+authority, got, wantOrigin+oidcCallbackPath)
		}
	})
}
