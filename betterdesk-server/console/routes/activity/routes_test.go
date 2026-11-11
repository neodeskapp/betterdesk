package activity

import (
	"database/sql"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/kit"
)

func TestParseInt10(t *testing.T) {
	nan := math.NaN()
	for text, want := range map[string]float64{
		"5": 5, "05": 5, " 7": 7, "\t\n7": 7, "\xc2\xa07": 7, "\xef\xbb\xbf7": 7, "\xe2\x80\x837": 7, "7 ": 7, "7abc": 7, "1.9": 1, "1e3": 1, "1,2": 1,
		"+3": 3, "-3": -3, "--3": nan, "+-3": nan, "-": nan, "": nan, "abc": nan, ".5": nan, "Infinity": nan, "NaN": nan,
		"0": 0, "-0": 0, "0x10": 0, "0X10": 0, "-0x10": 0, " +0xff": 0, "0x": 0, "0xzz": 0, "00x10": 0, "x10": nan, "1x10": 1,
		"99999999999999999999": 1e20, "9007199254740993": 9007199254740992, "1" + strings.Repeat("0", 400): math.Inf(1),
		"-1" + strings.Repeat("0", 400): math.Inf(-1),
	} {
		got := parseInt10(text)
		if math.IsNaN(want) != math.IsNaN(got) || !math.IsNaN(want) && got != want {
			t.Errorf("parseInt(%q, 10) = %v, want %v", text, got, want)
		}
	}
}

// What parseInt10 makes of any text: a whole number or NaN, the number
// of the digits it starts with, and never the hexadecimal reading.
func FuzzParseInt10(f *testing.F) {
	for _, seed := range []string{"5", " -12abc", "0x1f", "+0X", "1e5", "  9", "", "-", "9999999999999999999999"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got := parseInt10(text)
		if !math.IsNaN(got) && !math.IsInf(got, 0) && got != math.Trunc(got) {
			t.Fatalf("parseInt(%q, 10) = %v is no whole number", text, got)
		}
		digits := strings.TrimLeft(text, "0123456789")
		if lead := text[:len(text)-len(digits)]; lead != "" {
			want, _ := strconv.ParseFloat(lead, 64)
			if got != want {
				t.Fatalf("parseInt(%q, 10) = %v, want the number of its digits %v", text, got, want)
			}
		} else if text == "" || text[0] >= 'a' && text[0] <= 'z' {
			if !math.IsNaN(got) {
				t.Fatalf("parseInt(%q, 10) = %v, want NaN", text, got)
			}
		}
	})
}

func TestParsedInfo(t *testing.T) {
	text := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for _, tt := range []struct {
		name     string
		peer     peer
		hostname string // the JSON of info.hostname, "" for none
		fails    bool
	}{
		{name: "text: none", peer: peer{}},
		{name: "text: empty", peer: peer{info: text("")}},
		{name: "text: object", peer: peer{info: text(`{"hostname":"a"}`)}, hostname: `"a"`},
		{name: "text: number in it", peer: peer{info: text(`{"hostname":5}`)}, hostname: `5`},
		{name: "text: list", peer: peer{info: text(`[{"hostname":"a"}]`)}},
		{name: "text: number", peer: peer{info: text(`5`)}},
		{name: "text: text", peer: peer{info: text(`"{\"hostname\":\"a\"}"`)}},
		{name: "text: null", peer: peer{info: text(`null`)}, fails: true},
		{name: "text: no JSON", peer: peer{info: text(`{hostname}`)}, fails: true},
		{name: "document: NULL", peer: peer{document: true}},
		{name: "document: null", peer: peer{document: true, info: text(`null`)}},
		{name: "document: object", peer: peer{document: true, info: text(`{"hostname": "a"}`)}, hostname: `"a"`},
		{name: "document: list", peer: peer{document: true, info: text(`[1]`)}},
		{name: "document: number", peer: peer{document: true, info: text(`5`)}},
		{name: "document: flag", peer: peer{document: true, info: text(`true`)}},
		{name: "document: empty text", peer: peer{document: true, info: text(`""`)}},
		{name: "document: JSON in a text", peer: peer{document: true, info: text(`"{\"hostname\":[1]}"`)}, hostname: `[1]`},
		{name: "document: null in a text", peer: peer{document: true, info: text(`"null"`)}, fails: true},
		{name: "document: no JSON in a text", peer: peer{document: true, info: text(`"x"`)}, fails: true},
	} {
		info, err := tt.peer.parsedInfo()
		if (err != nil) != tt.fails {
			t.Errorf("%s: error %v, want one: %v", tt.name, err, tt.fails)
			continue
		}
		hostname := ""
		if name := info.Get("hostname"); name.Truthy() {
			out, _ := name.MarshalJSON()
			hostname = string(out)
		}
		if hostname != tt.hostname {
			t.Errorf("%s: hostname %s, want %s", tt.name, hostname, tt.hostname)
		}
	}
}

// The old console mounted the group at /api/bd too, for the route a
// device uploads with. There the three read routes step aside, so that
// GET /api/bd/commands and the like reach the routes they are meant for.
func TestTheReadRoutesStepAsideAtTheDeviceMount(t *testing.T) {
	router := kit.NewRouter()
	Register((&kit.Deps{Router: router, Log: kit.NewLogger("silent", true)}).At("/api/bd"))
	if len(router.Routes()) != 3 {
		t.Fatalf("%d routes registered, want the three read routes", len(router.Routes()))
	}
	for _, path := range []string{"/api/bd", "/api/bd/commands", "/api/bd/D1/top"} {
		rec := httptest.NewRecorder()
		if router.Serve(rec, httptest.NewRequest(http.MethodGet, path, nil)) {
			t.Errorf("GET %s was answered with %d: the route has to pass the request on", path, rec.Code)
		}
	}
}
