package serverinfo

import (
	"encoding/json"
	"fmt"
	"testing"
)

// idnaRun is one entry of a sweep: the characters From to To, and what
// Node made of a label with one of them in it. Kind "x": refused. Kind
// "v": kept as it is. Kind "m": the answer is given.
type idnaRun struct {
	From, To rune
	Kind     string
	Answer   string
}

func (r *idnaRun) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 4 {
		return fmt.Errorf("run with %d fields", len(raw))
	}
	if err := json.Unmarshal(raw[0], &r.From); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &r.To); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[2], &r.Kind); err != nil {
		return err
	}
	var answer *string
	if err := json.Unmarshal(raw[3], &answer); err != nil {
		return err
	}
	if answer != nil {
		r.Answer = *answer
	}
	return nil
}

type idnaVectors struct {
	Inside []idnaRun `json:"inside"`
	First  []idnaRun `json:"first"`
	Names  []struct {
		Name string  `json:"name"`
		Host *string `json:"host"`
	} `json:"names"`
}

// hostOf is the host Node's "new URL" reports for http://name/, or
// "refused".
func hostOf(name string) string {
	u, ok := parseJSURL("http://" + name + "/")
	if !ok {
		return "refused"
	}
	return u.hostname
}

// The sweeps hold Node's answer for a label with one character outside
// ASCII, for every such character up to U+1FFFF and a sample beyond. The
// port has no table of the rules for domain names, so a few answers
// differ. The test pins down how few, and that the port never accepts what
// Node refuses and never gives another name than Node: all it does is
// refuse some characters Unicode added since version 13.
func TestInternationalHostsAgainstNode(t *testing.T) {
	var v idnaVectors
	loadVectors(t, "idna", &v)
	for _, sweep := range []struct {
		name      string
		runs      []idnaRun
		label     func(string) string
		maxDiffer int
	}{
		{"inside a label", v.Inside, func(c string) string { return "a" + c + "b" }, 70},
		{"at the start of a label", v.First, func(c string) string { return c + "a" }, 225},
	} {
		total, differ := 0, 0
		kinds := map[string]int{}
		for _, run := range sweep.runs {
			for cp := run.From; cp <= run.To; cp++ {
				total++
				kinds[run.Kind]++
				label := sweep.label(string(cp))
				want := run.Answer
				switch run.Kind {
				case "x":
					want = "refused"
				case "v":
					encoded, ok := punycodeEncode([]rune(label))
					if !ok {
						t.Fatalf("U+%04X: no Punycode", cp)
					}
					want = "xn--" + encoded + ".example"
				}
				got := hostOf(label + ".example")
				if got == want {
					continue
				}
				differ++
				if got != "refused" {
					t.Errorf("%s, U+%04X: host %q, Node answers %q", sweep.name, cp, got, want)
				}
			}
		}
		if total < 130000 || kinds["x"] < 50000 || kinds["v"] < 60000 || kinds["m"] < 4000 {
			t.Fatalf("%s: %d characters (%v): the sweep is not complete", sweep.name, total, kinds)
		}
		if differ > sweep.maxDiffer {
			t.Errorf("%s: %d of %d characters are refused where Node accepts them, at most %d were known", sweep.name, differ, total, sweep.maxDiffer)
		}
		t.Logf("%s: %d of %d characters are refused where Node accepts them", sweep.name, differ, total)
	}
}

// Whole names: mixed scripts, right-to-left labels, joiners, "xn--" labels
// that are valid and that are not, percent-encoded letters, long labels.
func TestInternationalNamesAgainstNode(t *testing.T) {
	var v idnaVectors
	loadVectors(t, "idna", &v)
	atLeast(t, "names", len(v.Names), 200)
	accepted, refused := 0, 0
	for _, c := range v.Names {
		want := "refused"
		if c.Host != nil {
			want = *c.Host
			accepted++
		} else {
			refused++
		}
		if got := hostOf(c.Name); got != want {
			t.Errorf("host of %q is %q, Node answers %q", c.Name, got, want)
		}
	}
	if accepted < 100 || refused < 60 {
		t.Fatalf("%d accepted and %d refused names: too few", accepted, refused)
	}
}

func TestPunycodeRoundTrip(t *testing.T) {
	for _, label := range []string{"b\u00fccher", "m\u00fcnchen", "\u65e5\u672c\u8a9e", "\u043f\u0440\u0438\u043c\u0435\u0440", "a\u2603b", "\U0001f600", "\u00fc", "a\u00fc", "\u00fca", "fa\u00df"} {
		encoded, ok := punycodeEncode([]rune(label))
		if !ok {
			t.Fatalf("%q does not encode", label)
		}
		decoded, ok := punycodeDecode(encoded)
		if !ok || string(decoded) != label {
			t.Errorf("%q encodes to %q, which decodes to %q", label, encoded, string(decoded))
		}
	}
	if got, _ := punycodeEncode([]rune("b\u00fccher")); got != "bcher-kva" {
		t.Errorf("b\u00fccher encodes to %q", got)
	}
	for _, bad := range []string{"99999999", "zzzzzzzz", "\u00fc", "A"} {
		if decoded, ok := punycodeDecode(bad); ok {
			t.Errorf("%q decodes to %q", bad, string(decoded))
		}
	}
}
