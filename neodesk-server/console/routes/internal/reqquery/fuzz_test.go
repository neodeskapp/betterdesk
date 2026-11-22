package reqquery

import (
	"strings"
	"testing"
)

// FuzzQuery reads arbitrary query strings and uses everything the package
// offers on them. No query may stop the console, and a query written out
// again is plain form text.
func FuzzQuery(f *testing.F) {
	for _, seed := range []string{
		"", "a=1&b=2", "a=1&a=2", "a[]=1&a[k]=2", "a[toString]=1", "a%5Bb%5D=1", "[x]=1", "a[1]=x&a[0]=y", "a[b][c][d][e][f][g][h]=deep",
		"5=five&1=one", "a=%zz&b=%E4%B8", "guest=tok&t=tok&view_only=0", "a[99999999999]=1", "=&=&&&", "a[=1", "a]=1", "a[__proto__]=1",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if strings.ContainsAny(raw, " \r\n\x00") {
			return
		}
		q := of(raw)
		for _, name := range []string{"a", "guest", "t", "5", ""} {
			value := q.Get(name)
			value.Truthy()
			value.Defined()
			value.IsText()
			value.JSON()
			if text, ok := value.Text(); ok && !value.Defined() && text != "undefined" {
				t.Fatalf("text of the missing parameter %q of %q: %q", name, raw, text)
			}
		}
		q.Delete("t")
		if !q.Has("guest") {
			q.Set("guest", "x y&z")
		}
		q.Set("view_only", "1")
		if !q.Has("guest") || !q.Has("view_only") || q.Has("t") {
			t.Fatalf("Set and Delete did not take on %q", raw)
		}
		text, ok := q.Search()
		if !ok {
			return
		}
		if strings.ContainsAny(text, " \r\n\x00#?/\"<>") {
			t.Fatalf("Search of %q wrote %q", raw, text)
		}
		if !strings.Contains("&"+text+"&", "&view_only=1&") {
			t.Fatalf("Search of %q lost a parameter that was set: %q", raw, text)
		}
	})
}
