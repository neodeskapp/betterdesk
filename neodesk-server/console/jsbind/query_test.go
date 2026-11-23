package jsbind

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// TestQueryAsTheOldParserReadIt replays testdata/query.json: what qs made
// of the parameter "name" in about 6,000 query strings. Texts and lists of
// texts have to come out the same, and what qs made an object or a
// nested list of has to be complex here.
func TestQueryAsTheOldParserReadIt(t *testing.T) {
	raw, err := os.ReadFile("testdata/query.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Query string          `json:"query"`
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 6000 {
		t.Fatalf("query.json holds %d queries", len(vectors))
	}
	for _, vec := range vectors {
		got := Query(vec.Query, "name")
		switch vec.Kind {
		case "undefined":
			if !got.IsUndefined() {
				t.Errorf("Query(%q) = %s, want undefined", vec.Query, got.JSON())
			}
		case "string":
			var want string
			_ = json.Unmarshal(vec.Value, &want)
			if !got.IsString() || got.Str() != want {
				t.Errorf("Query(%q) = %s, want the text %q", vec.Query, got.JSON(), want)
			}
		case "list":
			var want []string
			_ = json.Unmarshal(vec.Value, &want)
			same := got.IsArray() && len(got.Items()) == len(want)
			for i := 0; same && i < len(want); i++ {
				same = got.Items()[i].IsString() && got.Items()[i].Str() == want[i]
			}
			if !same {
				t.Errorf("Query(%q) = %s, want the list %s", vec.Query, got.JSON(), vec.Value)
			}
		case "other":
			if !got.IsComplex() {
				t.Errorf("Query(%q) = %s, want a complex value", vec.Query, got.JSON())
			}
		default:
			t.Fatalf("unknown kind %q", vec.Kind)
		}
	}
}

func TestComplexQueryValueAsAParameter(t *testing.T) {
	v := Query("name[a][b]=1", "name")
	if !v.IsComplex() || !v.IsObject() || !v.Truthy() || v.IsArray() || v.IsString() {
		t.Fatalf("Query = %#v", v)
	}
	if _, err := Args(store.SQLite, 1, v); !errors.Is(err, ErrBindType) {
		t.Errorf("SQLite: %v, want ErrBindType", err)
	}
	args, err := Args(store.Postgres, 1, v)
	if err != nil || len(args) != 1 || args[0] != "{}" {
		t.Errorf("PostgreSQL: %v, %v", args, err)
	}
	if plain := Query("name=x", "name"); plain.IsComplex() {
		t.Error("a plain value is complex")
	}
}

func FuzzQuery(f *testing.F) {
	for _, seed := range []string{"", "name=a", "name=a&name=b", "name[]=a", "name[1]=b&name[0]=a", "name[x]=1", "name%5B%5D=%E2%9C%93", "name=%zz&name[", "a=1&&name"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		v := Query(query, "name")
		switch {
		case v.IsUndefined(), v.IsString(), v.IsComplex():
		case v.IsArray():
			if len(v.Items()) == 0 || len(v.Items()) > queryList {
				t.Errorf("Query(%q) is a list of %d values", query, len(v.Items()))
			}
			for _, item := range v.Items() {
				if !item.IsString() {
					t.Errorf("Query(%q) holds %s", query, item.JSON())
				}
			}
		default:
			t.Errorf("Query(%q) = %s", query, v.JSON())
		}
	})
}
