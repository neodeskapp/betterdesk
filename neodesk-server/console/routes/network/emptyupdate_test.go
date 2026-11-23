package network

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// An edit can't leave a target without a name, or without both a host and
// a URL, which a new target needs too; the stored target stays as it was
// (BUGS L418).
func TestUpdateKeepsNameAndAddress(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, names{})
		body := `{"name":"Printer","host":"10.1.2.3","check_type":"ping"}`
		rec := w.do("adm", "POST", "/api/network/targets", &body, "")
		if rec.Code != 201 {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		var id int64
		if err := db.QueryRow(context.Background(), "SELECT id FROM network_targets").Scan(&id); err != nil {
			t.Fatal(err)
		}
		path := "/api/network/targets/" + itoa(id)
		for b, want := range map[string]string{
			`{"name":""}`:              `{"error":"name must not be empty"}`,
			`{"name":"   "}`:           `{"error":"name must not be empty"}`,
			`{"host":""}`:              `{"error":"host or url is required"}`,
			`{"host":null,"url":null}`: `{"error":"host or url is required"}`,
		} {
			b := b
			rec := w.do("adm", "PATCH", path, &b, "")
			if rec.Code != 400 || rec.Body.String() != want {
				t.Errorf("PATCH %s: %d %s, want 400 %s", b, rec.Code, rec.Body, want)
			}
		}
		var name, host string
		if err := db.QueryRow(context.Background(), "SELECT name, host FROM network_targets").Scan(&name, &host); err != nil {
			t.Fatal(err)
		}
		if name != "Printer" || host != "10.1.2.3" {
			t.Errorf("after refused edits: name %q, host %q", name, host)
		}
		ok := `{"host":"","url":"http://10.1.2.3/"}`
		if rec := w.do("adm", "PATCH", path, &ok, ""); rec.Code != 200 {
			t.Errorf("host cleared with a url: %d %s", rec.Code, rec.Body)
		}
	})
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
