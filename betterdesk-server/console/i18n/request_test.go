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
	want := vectors(t, "pick.json")
	now := time.UnixMilli(int64(num(want, "now")))
	cases := list(want, "vectors")
	if len(cases) < 300 {
		t.Fatalf("only %d vectors", len(cases))
	}
	b, _ := loaded(t, copyDir(t, realLangDir), "en")
	cookies, throws := 0, 0
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
			throws++
			if sel.Err == nil {
				t.Errorf("%s: no error, Node threw %q", where(), str(c, "throws"))
			} else if sel.Err.Error() != str(c, "throws") {
				t.Errorf("%s: error %q, Node threw %q", where(), sel.Err, str(c, "throws"))
			}
			continue
		}
		if sel.Err != nil {
			t.Errorf("%s: error %v, Node picked %q", where(), sel.Err, str(c, "lang"))
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
	if cookies < 30 || throws < 1 {
		t.Errorf("the vectors set %d cookies and throw %d times; too few to mean anything", cookies, throws)
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
		{"an unknown query language gives the default, not the preference", "xx", "fr", "zh", "zh-TW", "en"},
		{"preference before cookie", "", "fr", "zh", "zh-TW", "fr"},
		{"an unknown preference gives the default, not the cookie", "", "xx", "zh", "zh-TW", "en"},
		{"cookie before header", "", "", "zh", "de", "zh"},
		{"an unknown cookie gives the default, not the header", "", "", "xx", "de", "en"},
		{"header", "", "", "", "fr-CH, fr;q=0.9, de;q=0.8", "fr"},
		{"header by q", "", "", "", "de;q=0.5, fr;q=0.9", "fr"},
		{"equal q keeps the order", "", "", "", "xx, de;q=1, fr;q=1.0", "de"},
		{"full tag before its language", "", "", "", "zh-TW", "zh-TW"},
		{"the tag is case-sensitive", "", "", "", "zh-tw", "zh"},
		{"unknown header languages", "", "", "", "xx, yy;q=0.5", "en"},
		{"q that cannot be read", "", "", "", "de;q=abc", "de"},
	}
	for _, c := range cases {
		got, err := PickLanguage(has, Text(c.query), c.preferred, Text(c.cookie), c.accept, "en")
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := PickLanguage(has, Choice{Truthy: true, Throws: true}, "de", Choice{}, "", "en"); err != ErrNotText {
		t.Errorf("a value that cannot be text: err %v", err)
	}
	if got, err := PickLanguage(has, Choice{}, "de", Choice{Truthy: true, Throws: true}, "", "en"); err != nil || got != "de" {
		t.Errorf("a bad cookie does not matter when the preference is used: %q, %v", got, err)
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
