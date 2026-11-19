package i18n

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// toBytes turns a recorded header (one character per byte) back into the
// bytes that were sent.
func toBytes(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteByte(byte(r))
	}
	return b.String()
}

// The language the Node middleware picked, and the cookie it set, for
// several hundred combinations of query, stored preference, cookie,
// Accept-Language and default language.
func TestRequestLanguageLikeNode(t *testing.T) {
	b, _ := loaded(t, copyDir(t, realLangDir), "en")
	node := vectors(t, "pick.json")
	now := time.UnixMilli(int64(num(node, "now")))
	// The reference the bug fixes rewrite these vectors with gives Node's
	// answers under Node's rules.
	for i, c := range list(node, "vectors") {
		lang, cookie, throws := referencePick(b, c, pickRules{}, now)
		wantCookie := ""
		if set := strs(c, "setCookie"); len(set) > 0 {
			wantCookie = set[0]
		}
		if lang != str(c, "lang") || cookie != wantCookie || throws != str(c, "throws") {
			t.Errorf("#%d query=%s: the reference gives %q, %q, %q; Node %q, %q, %q", i, short(str(c, "query")), lang, cookie, throws, str(c, "lang"), wantCookie, str(c, "throws"))
		}
	}

	want := fixedVectors(t, "pick.json")
	cases := list(want, "vectors")
	if len(cases) < 300 {
		t.Fatalf("only %d vectors", len(cases))
	}
	cookies := 0
	for i, c := range cases {
		b.DefaultLang = str(c, "def")
		r := &http.Request{URL: &url.URL{Path: "/p", RawQuery: str(c, "query")}, Header: http.Header{}}
		if v, ok := at(c, "cookie").(string); ok {
			r.Header.Set("Cookie", toBytes(v))
		}
		if v, ok := at(c, "accept").(string); ok {
			r.Header.Set("Accept-Language", toBytes(v))
		}
		preferred := ""
		if flag(c, "hasPref") {
			preferred = str(c, "pref")
		}
		sel := b.RequestLanguage(r, preferred, now)
		where := func() string {
			return "#" + strings.TrimSpace(strings.Join([]string{
				strconv.Itoa(i), "query=" + short(str(c, "query")), "pref=" + preferred, "cookie=" + str(c, "cookie"),
				"accept=" + short(str(c, "accept")), "default=" + str(c, "def")}, " "))
		}
		if str(c, "throws") != "" {
			t.Errorf("%s: the vector still fails with %q", where(), str(c, "throws"))
			continue
		}
		if sel.Lang != str(c, "lang") {
			t.Errorf("%s: language %q, Node picked %q", where(), sel.Lang, str(c, "lang"))
		}
		wantCookie := ""
		if set := strs(c, "setCookie"); len(set) > 0 {
			wantCookie = set[0]
			cookies++
		}
		if sel.SetCookie != wantCookie {
			t.Errorf("%s: Set-Cookie %q, Node sent %q", where(), sel.SetCookie, wantCookie)
		}
	}
	if cookies < 30 {
		t.Errorf("the vectors set %d cookies; too few to mean anything", cookies)
	}
}

// The names every JavaScript object has are no languages: not as ?lang=,
// not in Accept-Language and not to Validate (BUGS.md L1).
func TestHasIgnoresInheritedNames(t *testing.T) {
	b, _ := loaded(t, copyDir(t, "testdata/lang-upload"), "en")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"constructor", "toString", "valueOf", "hasOwnProperty", "__proto__", "isPrototypeOf", "toLocaleString"} {
		if b.Has(name) {
			t.Errorf("Has(%q) = true", name)
		}
		query := &http.Request{URL: &url.URL{Path: "/", RawQuery: "lang=" + name}, Header: http.Header{}}
		if sel := b.RequestLanguage(query, "", now); sel.Lang != "en" || sel.SetCookie != "" {
			t.Errorf("?lang=%s: language %q, cookie %q; want en and no cookie", name, sel.Lang, sel.SetCookie)
		}
		accept := &http.Request{URL: &url.URL{Path: "/"}, Header: http.Header{"Accept-Language": {name + ", de;q=0.5"}}}
		if sel := b.RequestLanguage(accept, "", now); sel.Lang != "de" {
			t.Errorf("Accept-Language %s, de;q=0.5: language %q, want de", name, sel.Lang)
		}
		if got := stringify(b.Validate(name).Object(), ""); got != `{"valid":false,"error":"Language not found"}` {
			t.Errorf("Validate(%q) = %s", name, got)
		}
	}
}

// A lang query value or a language cookie that is not a string counts as
// not given: the next source decides, and no j: cookie is set (BUGS.md L2,
// L3).
func TestRequestLanguageIgnoresValuesThatAreNotText(t *testing.T) {
	b, _ := loaded(t, copyDir(t, "testdata/lang-upload"), "en")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct{ query, cookie, accept, want string }{
		{"lang[toString]=de", "", "", "en"},
		{"lang[toString]=de", "betterdesk_lang=pl", "", "pl"},
		{"lang[]=de", "", "", "en"},
		{"lang[]=de", "betterdesk_lang=pl", "", "pl"},
		{"lang[0]=de", "", "", "en"},
		{"lang[0][0]=de", "", "", "en"},
		{"lang[x]=de", "", "pl", "pl"},
		{"lang=de&lang=pl", "", "", "en"},
		{"", `betterdesk_lang=j:["de"]`, "", "en"},
		{"", `betterdesk_lang=j:["de"]`, "pl", "pl"},
		{"", `betterdesk_lang=j:{"toString":1}`, "pl", "pl"},
		{"", "betterdesk_lang=j:5", "pl", "pl"},
		{"", `betterdesk_lang=j:"de"`, "pl", "de"},
		{"lang=de", "", "", "de"},
	}
	for _, c := range cases {
		r := &http.Request{URL: &url.URL{Path: "/", RawQuery: c.query}, Header: http.Header{}}
		if c.cookie != "" {
			r.Header.Set("Cookie", c.cookie)
		}
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		sel := b.RequestLanguage(r, "", now)
		wantCookie := ""
		if c.query == "lang=de" {
			wantCookie = LangCookie("de", false, now)
		}
		if sel.Lang != c.want || sel.SetCookie != wantCookie {
			t.Errorf("query %q, cookie %q, Accept-Language %q: language %q, Set-Cookie %q; want %q, %q", c.query, c.cookie, c.accept, sel.Lang, sel.SetCookie, c.want, wantCookie)
		}
	}
}

// A query, stored preference or cookie that names no language gives way
// to the next source, so a stale cookie or a wrong ?lang= no longer hides
// a good preference, cookie or Accept-Language (BUGS.md L4).
func TestPickLanguageSkipsValuesThatAreNoLanguage(t *testing.T) {
	b, _ := loaded(t, copyDir(t, "testdata/lang-upload"), "en")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct{ query, pref, cookie, accept, want string }{
		{"lang=xx", "de", "", "", "de"},
		{"lang=xx", "", "betterdesk_lang=pl", "", "pl"},
		{"lang=xx", "", "", "de", "de"},
		{"", "xx", "betterdesk_lang=pl", "", "pl"},
		{"", "xx", "", "de-AT", "de"},
		{"", "", "betterdesk_lang=xx", "pl;q=0.9, de", "de"},
		{"lang=xx", "yy", "betterdesk_lang=zz", "qq", "en"},
		{"lang=de", "pl", "betterdesk_lang=pl", "pl", "de"},
	}
	for _, c := range cases {
		r := &http.Request{URL: &url.URL{Path: "/", RawQuery: c.query}, Header: http.Header{}}
		if c.cookie != "" {
			r.Header.Set("Cookie", c.cookie)
		}
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		sel := b.RequestLanguage(r, c.pref, now)
		wantCookie := ""
		if c.query == "lang=de" {
			wantCookie = LangCookie("de", false, now)
		}
		if sel.Lang != c.want || sel.SetCookie != wantCookie {
			t.Errorf("query %q, preference %q, cookie %q, Accept-Language %q: language %q, Set-Cookie %q; want %q, %q",
				c.query, c.pref, c.cookie, c.accept, sel.Lang, sel.SetCookie, c.want, wantCookie)
		}
	}
}

func short(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func TestPickLanguageOrder(t *testing.T) {
	has := func(code string) bool {
		return code == "en" || code == "de" || code == "fr" || code == "zh" || code == "zh-TW"
	}
	cases := []struct {
		name                             string
		query, preferred, cookie, accept string
		want                             string
	}{
		{"nothing given", "", "", "", "", "en"},
		{"query wins", "de", "fr", "zh", "zh-TW", "de"},
		{"an unknown query language gives way to the preference", "xx", "fr", "zh", "zh-TW", "fr"},
		{"preference before cookie", "", "fr", "zh", "zh-TW", "fr"},
		{"an unknown preference gives way to the cookie", "", "xx", "zh", "zh-TW", "zh"},
		{"cookie before header", "", "", "zh", "de", "zh"},
		{"an unknown cookie gives way to the header", "", "", "xx", "de", "de"},
		{"nothing known gives the default", "xx", "yy", "zz", "qq", "en"},
		{"header", "", "", "", "fr-CH, fr;q=0.9, de;q=0.8", "fr"},
		{"header by q", "", "", "", "de;q=0.5, fr;q=0.9", "fr"},
		{"equal q keeps the order", "", "", "", "xx, de;q=1, fr;q=1.0", "de"},
		{"full tag before its language", "", "", "", "zh-TW", "zh-TW"},
		{"the tag is case-sensitive", "", "", "", "zh-tw", "zh"},
		{"unknown header languages", "", "", "", "xx, yy;q=0.5", "en"},
		{"q that cannot be read", "", "", "", "de;q=abc", "de"},
	}
	for _, c := range cases {
		if got := PickLanguage(has, c.query, c.preferred, c.cookie, c.accept, "en"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLangCookie(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.FixedZone("x", 3600))
	if got, want := LangCookie("zh-TW", false, now), "betterdesk_lang=zh-TW; Max-Age=31536000; Path=/; Expires=Wed, 10 Nov 2027 11:00:00 GMT; SameSite=Lax"; got != want {
		t.Errorf("LangCookie = %q, want %q", got, want)
	}
	if got, want := LangCookie(`j:["de"]`, true, now), "betterdesk_lang=j%3A%5B%22de%22%5D; Max-Age=31536000; Path=/; Expires=Wed, 10 Nov 2027 11:00:00 GMT; Secure; SameSite=Lax"; got != want {
		t.Errorf("LangCookie = %q, want %q", got, want)
	}
}

func TestParseFloat(t *testing.T) {
	cases := map[string]float64{
		"0.8": 0.8, " 0.3": 0.3, "\t0.4": 0.4, "1e2": 100, ".5": 0.5, "5.": 5, "0.5abc": 0.5, "+0.5": 0.5, "-1": -1,
		"0x10": 0, "5e-1": 0.5, "0.5e": 0.5, "1_0": 1, "Infinityx": math.Inf(1), "-Infinity": math.Inf(-1), "1e999": math.Inf(1),
	}
	for in, want := range cases {
		if got := parseFloat(in); got != want {
			t.Errorf("parseFloat(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "abc", ".", "-", "e5", "infinity", "+", "-.", " "} {
		if got := parseFloat(in); !math.IsNaN(got) {
			t.Errorf("parseFloat(%q) = %v, want NaN", in, got)
		}
	}
}

func TestDecodeURIComponent(t *testing.T) {
	ok := map[string]string{"de": "de", "%64%65": "de", "%E4%B8%AD": "中", "a%20b": "a b", "%F0%9F%98%80": "😀", "%c3%A9": "é"}
	for in, want := range ok {
		if got, valid := decodeURIComponent(in); !valid || got != want {
			t.Errorf("decodeURIComponent(%q) = %q, %v; want %q", in, got, valid, want)
		}
	}
	for _, in := range []string{"%", "%z", "%zz", "de%", "de%2", "%C3%28", "%ff", "%C3", "%E4%B8", "%ED%A0%80", "%C0%AF", "%80"} {
		if got, valid := decodeURIComponent(in); valid {
			t.Errorf("decodeURIComponent(%q) = %q, want a failure", in, got)
		}
	}
}

// V8's sort with an order that is not consistent: the result depends on
// the comparisons made, so a few fixed cases beside the recorded headers.
func TestV8SortStableForConsistentOrder(t *testing.T) {
	type item struct {
		key float64
		pos int
	}
	for _, n := range []int{0, 1, 2, 3, 31, 32, 63, 64, 65, 100, 257, 1000} {
		list := make([]item, n)
		seed := uint32(n*7919 + 1)
		for i := range list {
			seed = seed*1664525 + 1013904223
			list[i] = item{float64(seed >> 28), i}
		}
		v8Sort(list, func(a, b item) float64 { return a.key - b.key })
		for i := 1; i < n; i++ {
			if list[i-1].key > list[i].key || (list[i-1].key == list[i].key && list[i-1].pos > list[i].pos) {
				t.Fatalf("n=%d: not sorted or not stable at %d: %v then %v", n, i, list[i-1], list[i])
			}
		}
	}
}

// V8's Array.prototype.sort for the Accept-Language order, list by list:
// short lists, runs, many equal values, and lists long enough to merge and
// gallop, with and without q values that cannot be read.
func TestV8SortLikeNode(t *testing.T) {
	cases := list(vectors(t, "sort.json"))
	if len(cases) < 100 {
		t.Fatalf("only %d vectors", len(cases))
	}
	type item struct {
		q   float64
		pos int
	}
	long, unreadable := 0, 0
	for i, c := range cases {
		values := list(c, "q")
		items := make([]item, len(values))
		hasNaN := false
		for j, v := range values {
			q, ok := v.(float64)
			if !ok {
				q, hasNaN = math.NaN(), true
			}
			items[j] = item{q, j}
		}
		if len(items) >= 64 {
			long++
			if hasNaN {
				unreadable++
			}
		}
		v8Sort(items, func(a, b item) float64 {
			d := b.q - a.q
			if math.IsNaN(d) {
				return 0
			}
			return d
		})
		order := list(c, "order")
		for j := range items {
			if want := int(order[j].(float64)); items[j].pos != want {
				t.Errorf("list %d (%d values): position %d holds input %d, Node has %d", i, len(items), j, items[j].pos, want)
				break
			}
		}
	}
	if long < 50 || unreadable < 30 {
		t.Errorf("%d long lists, %d of them with unreadable values: too few", long, unreadable)
	}
}
