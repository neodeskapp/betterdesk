package webui

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// testdata/vectors.json.gz and routes_vectors.json.gz hold inputs with what
// the Node console's own code and packages answered for them (a fixed
// record made while it still existed): accepts, qs, encodeurl,
// decodeURI, path.extname, send's mime table, range-parser, and the helpers
// of lib/webUi.js.

type helperVectors struct {
	Accepts []struct {
		Accept *string  `json:"accept"`
		Offers []string `json:"offers"`
		Pick   string   `json:"pick"`
	} `json:"accepts"`
	ReturnParam []struct {
		Query string  `json:"query"`
		Value *string `json:"value"`
	} `json:"returnParam"`
	EncodeURL []struct{ In, Out string } `json:"encodeUrl"`
	Decode    []struct {
		In        string  `json:"in"`
		Component *string `json:"component"`
		URI       *string `json:"uri"`
	} `json:"decode"`
	Extname     []struct{ In, Out string }    `json:"extname"`
	ContentType []struct{ Name, Type string } `json:"contentType"`
	Ranges      []struct {
		Size   int64           `json:"size"`
		Header string          `json:"header"`
		Result json.RawMessage `json:"result"`
	} `json:"ranges"`
	HeaderPolicy []struct {
		Meta   *string `json:"meta"`
		Policy string  `json:"policy"`
	} `json:"headerPolicy"`
	ReadCSP []struct {
		HTML   string  `json:"html"`
		Policy *string `json:"policy"`
	} `json:"readCsp"`
	ReadBase []struct {
		HTML string  `json:"html"`
		Base *string `json:"base"`
	} `json:"readBase"`
	WithoutOldBase []struct{ In, Out string } `json:"withoutOldBase"`
	PublicPage     []struct {
		Sub  string `json:"sub"`
		Open bool   `json:"open"`
	} `json:"publicPage"`
	EscapeAttr []struct{ In, Out string } `json:"escapeAttr"`
}

func readGzipJSON(t testing.TB, name string, into any) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unpacked, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(unpacked).Decode(into); err != nil {
		t.Fatal(err)
	}
}

func loadHelperVectors(t *testing.T) helperVectors {
	t.Helper()
	var v helperVectors
	readGzipJSON(t, "vectors.json.gz", &v)
	return v
}

func atLeast(t *testing.T, got, want int) {
	t.Helper()
	if got < want {
		t.Fatalf("only %d vectors, want at least %d", got, want)
	}
}

func show(s *string) string {
	if s == nil {
		return "<none>"
	}
	return `"` + *s + `"`
}

func TestAcceptsMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).Accepts
	atLeast(t, len(vectors), 3000)
	picked := map[string]int{}
	for _, v := range vectors {
		r := &http.Request{Header: http.Header{}}
		if v.Accept != nil {
			r.Header.Set("Accept", latin1Bytes(*v.Accept))
		}
		got := httpx.Accepts(r, v.Offers...)
		picked[got]++
		if got != v.Pick {
			t.Errorf("Accept %s, offers %v: %q, Node picks %q", show(v.Accept), v.Offers, got, v.Pick)
		}
	}
	for _, kind := range []string{"", "text/html", "text/plain", "application/json"} {
		if picked[kind] < 50 {
			t.Errorf("only %d vectors pick %q", picked[kind], kind)
		}
	}
}

func TestQueryStringMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).ReturnParam
	atLeast(t, len(vectors), 1500)
	strings := 0
	for _, v := range vectors {
		got, ok := queryString(v.Query, "return")
		switch {
		case v.Value == nil && ok:
			t.Errorf("?%s: return = %q, Node has no plain string", clip(v.Query), got)
		case v.Value != nil && (!ok || got != *v.Value):
			t.Errorf("?%s: return = %q (%v), Node has %q", clip(v.Query), got, ok, *v.Value)
		}
		if v.Value != nil {
			strings++
		}
	}
	if strings < 100 || len(vectors)-strings < 200 {
		t.Errorf("%d vectors with a string and %d without: both kinds must be well covered", strings, len(vectors)-strings)
	}
}

func TestEncodeURLMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).EncodeURL
	atLeast(t, len(vectors), 1500)
	for _, v := range vectors {
		if got := httpx.EncodeURL(v.In); got != v.Out {
			t.Errorf("encodeURL(%q) = %q, Node gives %q", v.In, got, v.Out)
		}
	}
}

func TestDecodingMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).Decode
	atLeast(t, len(vectors), 2000)
	failures := 0
	for _, v := range vectors {
		got, ok := decodeURIComponent(v.In)
		if ok != (v.Component != nil) || ok && got != *v.Component {
			t.Errorf("decodeURIComponent(%q) = %q, %v; Node gives %s", v.In, got, ok, show(v.Component))
		}
		got, ok = decodeURI(v.In)
		if ok != (v.URI != nil) || ok && got != *v.URI {
			t.Errorf("decodeURI(%q) = %q, %v; Node gives %s", v.In, got, ok, show(v.URI))
		}
		if v.Component == nil {
			failures++
		}
	}
	if failures < 300 || len(vectors)-failures < 300 {
		t.Errorf("%d inputs that fail and %d that decode: both kinds must be well covered", failures, len(vectors)-failures)
	}
}

func TestExtnameMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).Extname
	atLeast(t, len(vectors), 500)
	for _, v := range vectors {
		if got := extname(v.In); got != v.Out {
			t.Errorf("extname(%q) = %q, Node gives %q", v.In, got, v.Out)
		}
	}
}

func TestStaticTypeMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).ContentType
	atLeast(t, len(vectors), 400)
	for _, v := range vectors {
		if got := staticType(v.Name); got != v.Type {
			t.Errorf("staticType(%q) = %q, Node gives %q", v.Name, got, v.Type)
		}
	}
}

func TestParseRangeMatchesNode(t *testing.T) {
	vectors := loadHelperVectors(t).Ranges
	atLeast(t, len(vectors), 2000)
	kinds := map[string]int{}
	for _, v := range vectors {
		ranges, satisfiable := parseRange(v.Size, v.Header)
		var code int
		if json.Unmarshal(v.Result, &code) == nil {
			// -1: no range lies in the file; -2: not a range header
			kinds["refused"]++
			if (code == -1) != !satisfiable || (code == -2) != (satisfiable && ranges == nil) {
				t.Errorf("parseRange(%d, %q) = %v, %v; Node gives %d", v.Size, v.Header, ranges, satisfiable, code)
			}
			continue
		}
		var want [][2]int64
		if err := json.Unmarshal(v.Result, &want); err != nil {
			t.Fatal(err)
		}
		kinds["ranges"]++
		got := make([][2]int64, 0, len(ranges))
		for _, r := range ranges {
			got = append(got, [2]int64{r.start, r.end})
		}
		if !satisfiable || !reflect.DeepEqual(got, want) {
			t.Errorf("parseRange(%d, %q) = %v, %v; Node gives %v", v.Size, v.Header, got, satisfiable, want)
		}
	}
	if kinds["refused"] < 300 || kinds["ranges"] < 300 {
		t.Errorf("coverage: %v", kinds)
	}
}

func TestPageHelpersMatchNode(t *testing.T) {
	v := loadHelperVectors(t)
	atLeast(t, len(v.HeaderPolicy)+len(v.ReadCSP)+len(v.ReadBase)+len(v.WithoutOldBase)+len(v.PublicPage)+len(v.EscapeAttr), 100)
	for _, c := range v.HeaderPolicy {
		meta := ""
		if c.Meta != nil {
			meta = *c.Meta
		}
		if got := HeaderPolicy(meta, false); got != c.Policy {
			t.Errorf("HeaderPolicy(%s) = %q, Node gives %q", show(c.Meta), got, c.Policy)
		}
	}
	for _, c := range v.ReadCSP {
		got, ok := readCSP(c.HTML)
		if ok != (c.Policy != nil) || ok && got != *c.Policy {
			t.Errorf("readCSP(%q) = %q, %v; Node gives %s", c.HTML, got, ok, show(c.Policy))
		}
	}
	for _, c := range v.ReadBase {
		got, ok := readBase(c.HTML)
		if ok != (c.Base != nil) || ok && got != *c.Base {
			t.Errorf("readBase(%q) = %q, %v; Node gives %s", c.HTML, got, ok, show(c.Base))
		}
	}
	for _, c := range v.WithoutOldBase {
		if got := withoutOldBase(c.In); got != c.Out {
			t.Errorf("withoutOldBase(%q) = %q, Node gives %q", c.In, got, c.Out)
		}
	}
	for _, c := range v.PublicPage {
		if got := isPublicPage(c.Sub); got != c.Open {
			t.Errorf("isPublicPage(%q) = %v, Node says %v", c.Sub, got, c.Open)
		}
	}
	for _, c := range v.EscapeAttr {
		if got := attrEscaper.Replace(c.In); got != c.Out {
			t.Errorf("escaping %q gives %q, Node gives %q", c.In, got, c.Out)
		}
	}
}

type routeVectors struct {
	Routes []struct {
		ID      string `json:"id"`
		Pattern string `json:"pattern"`
	} `json:"routes"`
	Cases []struct {
		Sub  string `json:"sub"`
		Page bool   `json:"page"`
		// Takes lists the patterns that match the decoded path; nil when
		// the path does not decode.
		Takes *[]int `json:"takes"`
	} `json:"cases"`
}

// Every pattern of the web app's build (routes.json as it was when the
// vectors were written) compiles here and takes the same paths as in
// JavaScript, pattern by pattern.
func TestRoutePatternsMatchNode(t *testing.T) {
	var v routeVectors
	readGzipJSON(t, "routes_vectors.json.gz", &v)
	if len(v.Routes) < 40 || len(v.Cases) < 2000 {
		t.Fatalf("%d routes and %d cases", len(v.Routes), len(v.Cases))
	}
	p := &page{}
	for _, route := range v.Routes {
		re, err := compileJSPattern(route.Pattern)
		if err != nil {
			t.Fatalf("the pattern of %s does not compile: %v", route.ID, err)
		}
		p.routes = append(p.routes, re)
	}
	pages, matched := 0, map[int]int{}
	for _, c := range v.Cases {
		if got := p.isPage(c.Sub); got != c.Page {
			t.Errorf("isPage(%q) = %v, Node says %v", c.Sub, got, c.Page)
		}
		if c.Page {
			pages++
		}
		if c.Takes == nil {
			continue
		}
		parts := strings.Split(c.Sub, "%25")
		for i, part := range parts {
			parts[i], _ = decodeURI(part)
		}
		decoded := strings.Join(parts, "%25")
		got := []int{}
		for i, re := range p.routes {
			if re.MatchString(decoded) {
				got = append(got, i)
				matched[i]++
			}
		}
		if !reflect.DeepEqual(got, *c.Takes) {
			t.Errorf("%q is taken by the patterns %v, in Node by %v", c.Sub, got, *c.Takes)
		}
	}
	if pages < 500 || len(v.Cases)-pages < 500 {
		t.Errorf("%d pages and %d other paths: both kinds must be well covered", pages, len(v.Cases)-pages)
	}
	for i, route := range v.Routes {
		if matched[i] == 0 {
			t.Errorf("no vector matches the pattern of %s", route.ID)
		}
	}
}

// The build in the repository, when there is one: its list of pages must
// be usable.
func TestRoutePatternsOfTheBuildCompile(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "web-ui", "build")
	raw, err := os.ReadFile(filepath.Join(dir, "routes.json"))
	if err != nil {
		t.Skip("no web-ui build in this checkout")
	}
	var list []struct {
		ID      string `json:"id"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, route := range list {
		if _, err := compileJSPattern(route.Pattern); err != nil {
			t.Errorf("the pattern of %s (%s) does not compile: %v", route.ID, route.Pattern, err)
		}
	}
	h := New(Options{Dir: dir, Production: true})
	if h.page == nil || len(h.page.routes) != len(list) {
		t.Errorf("the host did not load the %d pages of the build", len(list))
	}
}

func TestCompileJSPattern(t *testing.T) {
	rest, err := compileJSPattern(`^\/files(?:\/([^]*))?\/?$`)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"/files": true, "/files/a/b\nc": true, "/files/": true, "/filesx": false} {
		if got := rest.MatchString(path); got != want {
			t.Errorf("rest pattern on %q: %v, want %v", path, got, want)
		}
	}
	for _, source := range []string{`^\/a(?=b)`, `^\/a(?!b)`, `^\/(?<name>a)`, `^\/(a`, `^\/a\1`} {
		if re, err := compileJSPattern(source); err == nil {
			t.Errorf("%s compiled to %v, want an error", source, re)
		}
	}
	var _ *regexp.Regexp = rest
}
