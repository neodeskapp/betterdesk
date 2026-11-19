package bdapi

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The operator list maps only the API server's "acknowledged" to "accepted" and
// shows every other status as it is, also one named like a member every
// JavaScript object has; the id is the API server's when it is text or a
// number (BUGS RG10).
func TestNormalizeHelpRequest(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"id":21,"status":"acknowledged"}`, `{"id":"21","status":"accepted"}`},
		{`{"id":"a-1","status":"resolved"}`, `{"id":"a-1","status":"resolved"}`},
		{`{"id":22,"status":"constructor"}`, `{"id":"22","status":"constructor"}`},
		{`{"id":23,"status":"__proto__"}`, `{"id":"23","status":"__proto__"}`},
		{`{"id":24}`, `{"id":"24","status":"pending"}`},
		{`{"id":{"toString":"x"},"status":"pending"}`, `{"id":"","status":"pending"}`},
		{`{"status":{"toString":1}}`, `{"id":"","status":{"toString":1}}`},
	} {
		v, err := goapi.Parse([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		item, err := normalizeHelpRequest(v, 0)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		got, _ := httpx.Marshal(goapi.NewObject("id", item.obj.Get("id"), "status", item.obj.Get("status")))
		if string(got) != c.want {
			t.Errorf("%s: %s, want %s", c.in, got, c.want)
		}
	}
}
