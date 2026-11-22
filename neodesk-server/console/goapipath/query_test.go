package goapipath

import "testing"

func TestOneText(t *testing.T) {
	for query, want := range map[string]string{
		"a=x": "x", "a=": "", "a=%20x%20": " x ", "a=1&b=2": "1", "a=x&b[]=y": "x", "b=1&a=x#a=y": "x",
	} {
		if got, ok := QueryOf(query, "a").OneText(); !ok || got != want {
			t.Errorf("%s: %q, %v; want %q", query, got, ok, want)
		}
	}
	for _, query := range []string{"", "b=1", "a[]=x", "a=x&a=y", "a[x]=1", "a[toString]=1", "a[0]=x", "a[]=", "a[][toString]=1"} {
		if got, ok := QueryOf(query, "a").OneText(); ok {
			t.Errorf("%s: %q is one text", query, got)
		}
	}
}
