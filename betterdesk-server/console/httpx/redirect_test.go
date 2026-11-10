package httpx

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

type redirectVector struct {
	Name   string  `json:"name"`
	Method string  `json:"method"`
	Status int     `json:"status"`
	Target string  `json:"target"`
	Accept *string `json:"accept"`
	Vary   *string `json:"vary"`
	Answer struct {
		Status        int      `json:"status"`
		Location      []string `json:"location"`
		Vary          []string `json:"vary"`
		ContentType   []string `json:"contentType"`
		ContentLength []string `json:"contentLength"`
		Body          string   `json:"body"`
	} `json:"answer"`
}

// Every recorded answer of Express's res.redirect(status, target)
// (testdata/redirect_vectors.json, written by
// .ui-rework/go/tools/vectors/express-answers.js).
func TestRedirectMatchesExpress(t *testing.T) {
	raw, err := os.ReadFile("testdata/redirect_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []redirectVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) < 500 {
		t.Fatalf("only %d vectors", len(file.Vectors))
	}
	seen := map[string]int{}
	for _, v := range file.Vectors {
		if v.Target == "back" {
			// Express reads "back" as the Referer header (here: none, so "/").
			// No route of the console redirects there; Go takes the word as
			// an address.
			rec := httptest.NewRecorder()
			Redirect(rec, httptest.NewRequest(v.Method, "/redirect", nil), v.Status, v.Target)
			if got := rec.Header().Get("Location"); got != "back" || v.Answer.Location[0] != "/" {
				t.Errorf("%s: Location %q, Express sends %q", v.Name, got, v.Answer.Location)
			}
			continue
		}
		r := httptest.NewRequest(v.Method, "/redirect", nil)
		if v.Accept != nil {
			r.Header.Set("Accept", *v.Accept)
		}
		rec := httptest.NewRecorder()
		if v.Vary != nil {
			rec.Header().Set("Vary", *v.Vary)
		}
		Redirect(rec, r, v.Status, v.Target)

		want, err := base64.StdEncoding.DecodeString(v.Answer.Body)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Code != v.Answer.Status {
			t.Errorf("%s: status %d, Express answers %d", v.Name, rec.Code, v.Answer.Status)
		}
		if got := rec.Body.String(); got != string(want) {
			t.Errorf("%s: body %q, Express sends %q", v.Name, got, want)
		}
		h := rec.Header()
		for name, express := range map[string][]string{
			"Location": v.Answer.Location, "Vary": v.Answer.Vary, "Content-Type": v.Answer.ContentType, "Content-Length": v.Answer.ContentLength,
		} {
			if got := h.Values(name); !reflect.DeepEqual(got, express) && (len(got) > 0 || len(express) > 0) {
				t.Errorf("%s: %s = %q, Express sends %q", v.Name, name, got, express)
			}
		}
		switch {
		case len(want) == 0 && v.Method != http.MethodHead:
			seen["no body"]++
		case len(v.Answer.ContentType) == 1 && v.Answer.ContentType[0] == "text/html; charset=utf-8":
			seen["html"]++
		default:
			seen["text"]++
		}
		if v.Answer.Location[0] != v.Target {
			seen["encoded"]++
		}
	}
	for _, kind := range []string{"no body", "html", "text", "encoded"} {
		if seen[kind] < 40 {
			t.Errorf("only %d vectors of the kind %q", seen[kind], kind)
		}
	}
}

func TestAddVary(t *testing.T) {
	cases := []struct {
		have  []string
		field string
		want  string
	}{
		{nil, "Accept", "Accept"},
		{[]string{"Origin"}, "Accept", "Origin, Accept"},
		{[]string{"Origin", "Accept-Encoding"}, "Accept", "Origin, Accept-Encoding, Accept"},
		{[]string{"accept"}, "Accept", "accept"},
		{[]string{"Origin,ACCEPT"}, "Accept", "Origin,ACCEPT"},
		{[]string{"*"}, "Accept", "*"},
		{[]string{"Origin, *"}, "Accept", "*"},
		{[]string{"Accept-Encoding"}, "Accept", "Accept-Encoding, Accept"},
	}
	for _, c := range cases {
		h := http.Header{}
		for _, v := range c.have {
			h.Add("Vary", v)
		}
		AddVary(h, c.field)
		if got := h.Values("Vary"); len(got) != 1 || got[0] != c.want {
			t.Errorf("AddVary(%q, %q) = %q, want one header %q", c.have, c.field, got, c.want)
		}
	}
}

// EncodeURL takes text as UTF-8, and bytes that are no UTF-8 become the
// replacement character one by one.
func TestEncodeURLTakesUTF8(t *testing.T) {
	for in, want := range map[string]string{
		"/login?return=%2Fdevices": "/login?return=%2Fdevices",
		"/a b":                     "/a%20b",
		"/ä/日本":                    "/%C3%A4/%E6%97%A5%E6%9C%AC",
		"/x?y=<z>&q=\"1\"":         "/x?y=%3Cz%3E&q=%221%22",
		"/100%":                    "/100%25",
		"/%zz":                     "/%25zz",
		"/%4z":                     "/%254z",
		"/%4":                      "/%4",
		"/%%41":                    "/%25%2541",
		"/%é":                      "/%25%C3%A9",
		"/\xff\xfe":                "/%EF%BF%BD%EF%BF%BD",
		"":                         "",
	} {
		if got := EncodeURL(in); got != want {
			t.Errorf("EncodeURL(%q) = %q, want %q", in, got, want)
		}
	}
}
