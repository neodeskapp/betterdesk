package network

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// Whatever a query string holds, a member read from it is nothing, a
// string, a list or an object, and its parts agree with each other.
func FuzzQuery(f *testing.F) {
	for _, seed := range []string{
		"limit=5", "limit=5&limit=6", "limit[]=5", "limit[0]=5&limit[1]=6", "limit[a]=5", "limit[a][b]=5", "limit", "limit=", "limit=%zz",
		"limit=+5+", "LIMIT=5", "limit%5B%5D=5", "&&limit=1&&", "limit=1&limit[a]=2", "limit[=5", "limit]=5", "=5", "limit==5",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		v := query(raw, "limit")
		switch v.kind {
		case queryUndefined:
			if len(v.items) != 0 {
				t.Fatalf("%q: nothing, with %+v", raw, v)
			}
		case queryString:
			if len(v.items) != 1 || v.text != v.items[0] {
				t.Fatalf("%q: a string, with %+v", raw, v)
			}
		case queryList, queryObject:
			if len(v.items) == 0 {
				t.Fatalf("%q: a list or an object, with %+v", raw, v)
			}
		default:
			t.Fatalf("%q: kind %d", raw, v.kind)
		}
	})
}

// Whatever JSON a body holds, its members can be read as the routes read
// them, and what goes back to the client is JSON again.
func FuzzBodyValues(f *testing.F) {
	for _, seed := range []string{
		`{"host":"0.0.0.0","port":5000,"timeout":"1000ms"}`, `{"host":["a",[1,null,{"b":true}]],"port":1e400,"timeout":-0}`,
		`{"host":{"b":1.50,"a":"xé","10":true,"2":null},"port":"0x50","timeout":[]}`, `{"host":"\ud83d","port":1E-7,"timeout":{}}`,
		`{"host":null,"port":false,"timeout":"  7  "}`, `[]`, `{}`, `{"host":"a","host":"b"}`, `{"host":123456789012345678901234567890}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var seen bool
		handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = true
			body := kit.Body(r)
			for _, key := range []string{"host", "port", "timeout"} {
				v := body.Get(key)
				_ = text(v)
				// A target takes only a whole number in range
				if n, ok := wholeNumber(v, maxPort); ok && n != 0 && (n < 1 || n > maxPort || float64(n) != math.Trunc(float64(n))) {
					t.Fatalf("%s: member %s is taken as the port %s", raw, key, n)
				} else if !ok && n != 0 {
					t.Fatalf("%s: member %s is refused but read as %s", raw, key, n)
				}
				if n := parseInt(v); !n.IsNaN() && n.String() == "" {
					t.Fatalf("%s: a number without text", raw)
				}
				if !v.Exists() {
					continue
				}
				if out := stringify(v); !json.Valid(out) {
					t.Fatalf("%s: member %s goes back as %s", raw, key, out)
				}
				answer, err := object{{"host", stringify(v)}, {"port", parseInt(v)}}.MarshalJSON()
				if err != nil || !json.Valid(answer) {
					t.Fatalf("%s: answer %s, %v", raw, answer, err)
				}
			}
		}))
		r := httptest.NewRequest("POST", "/api/network/tcp", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if raw == `{}` && !seen {
			t.Fatal("an empty object did not reach the handler")
		}
	})
}
