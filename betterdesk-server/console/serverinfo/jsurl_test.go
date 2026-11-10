package serverinfo

import (
	"testing"
)

type urlVectors struct {
	Parse []struct {
		In  string `json:"in"`
		Out *struct {
			Protocol string `json:"protocol"`
			Hostname string `json:"hostname"`
			Port     string `json:"port"`
			Pathname string `json:"pathname"`
			Search   string `json:"search"`
		} `json:"out"`
	} `json:"parse"`
}

func TestParseJSURLAgainstNode(t *testing.T) {
	var v urlVectors
	loadVectors(t, "urls", &v)
	atLeast(t, "parse", len(v.Parse), 2000)
	m := &mismatches{t: t}
	parsed, refused := 0, 0
	for _, c := range v.Parse {
		got, ok := parseJSURL(c.In)
		if c.Out == nil {
			refused++
			if ok {
				m.errorf("parseJSURL(%q) = %+v, Node refuses it", c.In, got)
			}
			continue
		}
		parsed++
		want := jsURL{protocol: c.Out.Protocol, hostname: c.Out.Hostname, port: c.Out.Port, pathname: c.Out.Pathname, search: c.Out.Search}
		if !ok {
			m.errorf("parseJSURL(%q) refused, Node answers %+v", c.In, want)
			continue
		}
		if got != want {
			m.errorf("parseJSURL(%q) = %+v, Node answers %+v", c.In, got, want)
		}
	}
	if parsed < 800 || refused < 400 {
		t.Fatalf("%d parsed and %d refused: too few", parsed, refused)
	}
}

func TestURLPort(t *testing.T) {
	for _, c := range []struct {
		text, port string
		ok         bool
	}{
		{"http://127.0.0.1:21114/api", "21114", true},
		{"http://127.0.0.1:021121/api", "21121", true},
		{"http://host/api", "", true},
		{"http://host:80/api", "", true},
		{"https://host:443", "", true},
		{"https://host:80", "80", true},
		{" http://host:5 ", "5", true},
		{"localhost:21114/api", "", true},
		{"127.0.0.1:21114/api", "", false},
		{"http://host:99999", "", false},
		{"not a url", "", false},
		{"", "", false},
	} {
		port, ok := URLPort(c.text)
		if port != c.port || ok != c.ok {
			t.Errorf("URLPort(%q) = %q, %v; want %q, %v", c.text, port, ok, c.port, c.ok)
		}
	}
}
