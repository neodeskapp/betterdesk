package goapipath

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The files under testdata were written by the Node console's own code
// (testdata/nodeoracle). Each test replays one of them.

func loadVectors(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestPathVectors(t *testing.T) {
	var vectors []struct {
		Fn    string  `json:"fn"`
		Value string  `json:"value"`
		Label string  `json:"label"`
		OK    *string `json:"ok"`
		Error string  `json:"error"`
	}
	loadVectors(t, "paths.json", &vectors)
	if len(vectors) < 4000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	counts := map[string]int{}
	for _, v := range vectors {
		var got string
		var err error
		switch v.Fn {
		case "id":
			got, err = AssertSafeAPIID(v.Value, v.Label)
		case "segment":
			got, err = SafeSegment(v.Value, v.Label)
		case "path":
			got, err = AssertSafeGoAPIRelativePath(v.Value)
		default:
			t.Fatalf("unknown function %q", v.Fn)
		}
		if v.OK != nil {
			counts[v.Fn+" accepted"]++
			if err != nil || got != *v.OK {
				t.Errorf("%s(%q, %q) = %q, %v; Node returns %q", v.Fn, v.Value, v.Label, got, err, *v.OK)
			}
			continue
		}
		counts[v.Fn+" refused"]++
		if err == nil || err.Error() != v.Error {
			t.Errorf("%s(%q, %q) = %q, %v; Node throws %q", v.Fn, v.Value, v.Label, got, err, v.Error)
		}
		if v.Fn != "path" && !IsInvalid(err) {
			t.Errorf("%s(%q): IsInvalid(%v) is false", v.Fn, v.Value, err)
		}
	}
	for _, kind := range []string{"id accepted", "id refused", "segment accepted", "segment refused", "path accepted", "path refused"} {
		if counts[kind] < 20 {
			t.Errorf("only %d vectors of the kind %q", counts[kind], kind)
		}
	}
}

// rawBytes turns a vector's text into the bytes of a request: every
// character stands for one byte.
func rawBytes(s string) string {
	out := make([]byte, 0, len(s))
	for _, c := range s {
		out = append(out, byte(c))
	}
	return string(out)
}

func TestQueryVectors(t *testing.T) {
	var vectors [][]json.RawMessage
	loadVectors(t, "query.json", &vectors)
	if len(vectors) < 6000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	kinds := map[string]int{}
	failures := 0
	for _, v := range vectors {
		var raw string
		var want [][]any
		if err := json.Unmarshal(v[0], &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(v[1], &want); err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"a", "b"} {
			value := QueryOf(rawBytes(raw), name)
			text, err := value.Text()
			exists, truthy := want[i][0].(bool), want[i][1].(bool)
			problem := ""
			switch {
			case value.Exists() != exists:
				problem = "Exists"
			case value.Truthy() != truthy:
				problem = "Truthy"
			case want[i][2] == nil:
				kinds["throws"]++
				if err == nil || err.Error() != want[i][3].(string) {
					problem = "Text error"
				}
			default:
				switch {
				case !exists:
					kinds["missing"]++
				case value.node.kind == queryString:
					kinds["text"]++
				case value.node.kind == queryArray:
					kinds["list"]++
				default:
					kinds["object"]++
				}
				if err != nil || text != want[i][2].(string) {
					problem = "Text"
				}
			}
			if problem != "" {
				failures++
				if failures <= 20 {
					t.Errorf("query %q, name %s: %s differs: got exists=%v truthy=%v text=%q err=%v, Node has %v", raw, name, problem, value.Exists(), value.Truthy(), text, err, want[i])
				}
			}
		}
	}
	if failures > 20 {
		t.Errorf("%d values differ", failures)
	}
	for _, kind := range []string{"missing", "text", "list", "object", "throws"} {
		if kinds[kind] < 50 {
			t.Errorf("only %d vectors of the kind %q", kinds[kind], kind)
		}
	}
}

func TestJavaScriptVectors(t *testing.T) {
	var vectors []struct {
		Fn     string `json:"fn"`
		Text   string `json:"text"`
		Number string `json:"number"`
		Limit  string `json:"limit"`
		Offset string `json:"offset"`
		Cut    string `json:"cut"`
		Form   string `json:"form"`
	}
	loadVectors(t, "js.json", &vectors)
	if len(vectors) < 2500 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, v := range vectors {
		switch v.Fn {
		case "parseInt":
			n := ParseInt(v.Text)
			if number := NumberText(n); number != v.Number {
				t.Errorf("ParseInt(%q) = %s, Node has %s", v.Text, number, v.Number)
			}
			// Math.min(parseInt(text) || 50, 200) and parseInt(text) || 0
			limit, offset := n, n
			if n == 0 || math.IsNaN(n) {
				limit, offset = 50, 0
			}
			limit = math.Min(limit, 200)
			if got := NumberText(limit); got != v.Limit {
				t.Errorf("limit of %q = %s, Node has %s", v.Text, got, v.Limit)
			}
			if got := NumberText(offset); got != v.Offset {
				t.Errorf("offset of %q = %s, Node has %s", v.Text, got, v.Offset)
			}
		case "slice":
			cut := Slice(v.Text, 64)
			if cut != v.Cut {
				t.Errorf("Slice(%q, 64) = %q, Node has %q", v.Text, cut, v.Cut)
			}
			if got := FormEncode("status", cut, "org id", Slice(v.Text, 5)); got != v.Form {
				t.Errorf("FormEncode of %q = %q, Node has %q", v.Text, got, v.Form)
			}
		default:
			t.Fatalf("unknown function %q", v.Fn)
		}
	}
}

func TestNumberText(t *testing.T) {
	for n, want := range map[float64]string{
		0: "0", 1: "1", -17: "-17", 1e20: "100000000000000000000", 1e21: "1e+21", 1.5e25: "1.5e+25", -1e21: "-1e+21",
		0.5: "0.5", 1e-6: "0.000001", 1e-7: "1e-7", 1.5e-10: "1.5e-10", 9007199254740992: "9007199254740992",
	} {
		if got := NumberText(n); got != want {
			t.Errorf("NumberText(%v) = %q, want %q", n, got, want)
		}
	}
	if got := NumberText(math.Copysign(0, -1)); got != "0" {
		t.Errorf("NumberText(-0) = %q", got)
	}
	if NumberText(math.NaN()) != "NaN" || NumberText(math.Inf(1)) != "Infinity" || NumberText(math.Inf(-1)) != "-Infinity" {
		t.Error("NaN and the infinities have their names")
	}
}
