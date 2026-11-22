package i18n

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestKeysOnlyReads: Keys of an object with number-like keys gives
// JavaScript's order without changing the object, so requests that read
// one language object at the same time do not race (BUGS.md RV192).
func TestKeysOnlyReads(t *testing.T) {
	o := NewObject()
	for _, k := range []string{"404", "x", "2", "b"} {
		o.Set(k, k)
	}
	before := *o
	before.m = nil
	if got := strings.Join(o.Keys(), ","); got != "2,404,x,b" {
		t.Errorf("keys %s", got)
	}
	after := *o
	after.m = nil
	if !reflect.DeepEqual(before, after) {
		t.Errorf("Keys changed the object: %+v, before %+v", after, before)
	}
	parsed, err := parseJSON(`{"404":1,"x":2,"2":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(parsed.(*Object).Keys(), ","); got != "2,404,x" {
		t.Errorf("parsed keys %s", got)
	}
	c := o.clone()
	c.Set("1", "one")
	if got := strings.Join(c.Keys(), ",") + " " + strings.Join(o.Keys(), ","); got != "1,2,404,x,b 2,404,x,b" {
		t.Errorf("clone and original: %s", got)
	}

	dir := copyDir(t, "testdata/lang-upload")
	for _, name := range []string{"en.json", "de.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"errors":{"404":"a","500":"b","x":"c","2":"d"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := loaded(t, dir, "en")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = stringify(b.Merged("en"), "")
				_ = b.Validate("de")
			}
		}()
	}
	wg.Wait()
}
