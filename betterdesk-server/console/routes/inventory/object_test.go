package inventory

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
)

// What {...info, hostname: ..., ...} and JSON.stringify make of the info
// of a device, for each kind of value the info can be. The texts are what
// Node wrote (node_device_sqlite.json has them for whole uploads).
func TestSpreadAndJSON(t *testing.T) {
	for _, tt := range []struct{ info, want string }{
		{`{}`, `{"hostname":"pc","memory_mb":2}`},
		{`{"b":1,"2":"two","a":2,"1":"one","hostname":"old"}`, `{"1":"one","2":"two","b":1,"a":2,"hostname":"pc","memory_mb":2}`},
		{`{"memory_mb":7,"os":"old"}`, `{"memory_mb":2,"hostname":"pc"}`},
		{`[1,"x",null]`, `{"0":1,"1":"x","2":null,"hostname":"pc","memory_mb":2}`},
		{`"a😀"`, `{"0":"a","1":"\ud83d","2":"\ude00","hostname":"pc","memory_mb":2}`},
		{`5`, `{"hostname":"pc","memory_mb":2}`},
		{`null`, `{"hostname":"pc","memory_mb":2}`},
		{`{"__proto__":{"x":1}}`, `{"__proto__":{"x":1},"hostname":"pc","memory_mb":2}`},
	} {
		info, err := jsjson.Parse(tt.info)
		if err != nil {
			t.Fatal(err)
		}
		o := spread(info)
		o.set("hostname", jsjson.OfText("pc"))
		o.set("os", jsjson.Value{})
		o.set("memory_mb", jsjson.OfNumber(2))
		if got := o.json(); got != tt.want {
			t.Errorf("info %s: %s, want %s", tt.info, got, tt.want)
		}
	}
}

// What the spread makes of any JSON value, with the members the upload
// sets: JSON that reads back as an object, with the value's members, then
// the new ones, in the order JavaScript keeps them, and nothing for a
// member left undefined.
func FuzzSpread(f *testing.F) {
	for _, seed := range []string{`{}`, `{"b":1,"2":2,"a":{"c":[1]}}`, `[1,[2],{"x":null}]`, `"a😀\ud83d"`, `5`, `true`, `null`, `{"hostname":1,"0":0}`, `{"a":1,"a":2}`} {
		f.Add(seed, "pc", 1.5)
	}
	f.Fuzz(func(t *testing.T, text, hostname string, megabytes float64) {
		info, err := jsjson.Parse(text)
		if err != nil {
			return
		}
		o := spread(info)
		o.set("hostname", jsjson.OfText(hostname))
		o.set("os", jsjson.Value{})
		o.set("memory_mb", jsjson.OfNumber(megabytes))
		out, err := jsjson.Parse(o.json())
		if err != nil {
			t.Fatalf("info %s: %s is no JSON: %v", text, o.json(), err)
		}

		var keys []string
		value := map[string]jsjson.Value{}
		switch info.Kind() {
		case jsjson.Object:
			for _, key := range info.Keys() {
				keys = append(keys, key)
				value[key] = info.Get(key)
			}
		case jsjson.Array:
			for i, item := range info.Items() {
				keys = append(keys, fmt.Sprint(i))
				value[fmt.Sprint(i)] = item
			}
		case jsjson.String:
			for i, unit := range jsjson.Units(info.Str()) {
				keys = append(keys, fmt.Sprint(i))
				value[fmt.Sprint(i)] = jsjson.OfText(unit)
			}
		}
		if _, ok := value["os"]; ok {
			keys = deleted(keys, "os")
		}
		for _, key := range []string{"hostname", "memory_mb"} {
			if _, ok := value[key]; !ok {
				keys = append(keys, key)
			}
		}
		value["hostname"], value["memory_mb"] = jsjson.OfText(hostname), jsjson.OfNumber(megabytes)
		if got := out.Keys(); !reflect.DeepEqual(got, keys) {
			t.Fatalf("info %s: keys %q, want %q", text, got, keys)
		}
		for _, key := range keys {
			got, _ := out.Get(key).MarshalJSON()
			want, _ := value[key].MarshalJSON()
			if string(got) != string(want) {
				t.Fatalf("info %s: member %q is %s, want %s", text, key, got, want)
			}
		}
	})
}

func deleted(keys []string, key string) []string {
	var out []string
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}
