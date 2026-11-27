package settings_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// outbox is a transport that keeps what it is handed.
type outbox struct{ sent []mail.Outgoing }

func (o *outbox) Send(_ context.Context, _ mail.Settings, m mail.Outgoing) error {
	o.sent = append(o.sent, m)
	return nil
}
func (o *outbox) Verify(context.Context, mail.Settings) error { return nil }

// The sender and footer settings: saved, refused field by field, and in
// the mails.
func TestMailIdentity(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		const path = "/api/settings/email/identity"
		jsonType := []string{"Content-Type", "application/json"}
		if res := w.As("operator").Do("GET", path, ""); res.Status != http.StatusForbidden {
			t.Errorf("operator: %d", res.Status)
		}
		if res := admin.Do("GET", path, ""); res.Status != http.StatusOK || !strings.Contains(res.Body, `"default_from_name":"NeoDesk"`) {
			t.Errorf("first read: %d %s", res.Status, res.Body)
		}
		for body, field := range map[string]string{
			`{"support_email":"not an address"}`:    "support_email",
			`{"support_url":"javascript:alert(1)"}`: "support_url",
			`{"organisation":"a\nb"}`:               "organisation",
		} {
			if res := admin.Do("PUT", path, body, jsonType...); res.Status != http.StatusBadRequest || !strings.Contains(res.Body, `"field":"`+field+`"`) {
				t.Errorf("save %s: %d %s", body, res.Status, res.Body)
			}
		}
		res := admin.Do("PUT", path, `{"from_name":"Acme Helpdesk","organisation":"Acme GmbH","support_email":"it@acme.example","support_url":"https://acme.example/help"}`, jsonType...)
		if res.Status != http.StatusOK || !strings.Contains(res.Body, `"organisation":"Acme GmbH"`) {
			t.Fatalf("save: %d %s", res.Status, res.Body)
		}
		if rows := w.AuditRows(); len(rows) != 1 || !strings.HasPrefix(rows[0], "mail_identity_saved|") {
			t.Errorf("audit %q", rows)
		}
	})
}

// Every mail has a preview, in the language asked for and in the light or
// dark palette, with its images inline; "Send me this mail" sends it to
// the admin's own address.
func TestMailPreviews(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := worldWith(t, db, map[string]string{"PANEL_PUBLIC_URL": "https://desk.example"})
		admin := w.As("admin")
		box := &outbox{}
		w.Server.Deps().Mail = mail.New(mail.Options{DB: w.DB, Transport: box, Getenv: func(n string) string {
			return map[string]string{"PANEL_PUBLIC_URL": "https://desk.example"}[n]
		}})
		res := admin.Do("GET", "/api/settings/email/previews", "")
		var list struct {
			Kinds []struct{ Kind string } `json:"kinds"`
		}
		if err := json.Unmarshal([]byte(res.Body), &list); err != nil || len(list.Kinds) != 18 {
			t.Fatalf("list: %d %s", res.Status, res.Body)
		}
		for _, k := range list.Kinds {
			for _, scheme := range []string{"light", "dark"} {
				res := admin.Do("GET", "/api/settings/email/previews/"+k.Kind+"?lang=de&scheme="+scheme, "")
				var p struct{ Subject, HTML, Text, Lang string }
				if err := json.Unmarshal([]byte(res.Body), &p); err != nil || res.Status != http.StatusOK || p.Subject == "" || p.Text == "" {
					t.Fatalf("%s %s: %d %.300s", k.Kind, scheme, res.Status, res.Body)
				}
				if strings.Contains(p.HTML, `src="cid:`) || !strings.Contains(p.HTML, `src="data:image/png;base64,`) {
					t.Errorf("%s: the images aren't inline", k.Kind)
				}
				if (scheme == "dark") != strings.Contains(p.HTML, "@media all {") {
					t.Errorf("%s %s: the palette isn't pinned", k.Kind, scheme)
				}
			}
		}
		if res := admin.Do("GET", "/api/settings/email/previews/nothing", ""); res.Status != http.StatusNotFound {
			t.Errorf("unknown kind: %d", res.Status)
		}
		// Without an address of their own the admin is told so
		jsonType := []string{"Content-Type", "application/json"}
		if res := admin.Do("POST", "/api/settings/email/previews/invite/send", `{"lang":"en"}`, jsonType...); res.Status != http.StatusConflict || !strings.Contains(res.Body, "no_address") {
			t.Errorf("no address: %d %s", res.Status, res.Body)
		}
		exec(t, w.DB, `UPDATE users SET email = 'admin@example.test' WHERE username = 'e2e_admin'`)
		exec(t, w.DB, `INSERT INTO settings (key, value) VALUES ('smtp_config', '{"host":"smtp.example.test","from":"desk@example.test"}')`)
		if res := admin.Do("POST", "/api/settings/email/previews/alert/send", `{"lang":"en"}`, jsonType...); res.Status != http.StatusOK {
			t.Fatalf("send: %d %s", res.Status, res.Body)
		}
		if len(box.sent) != 1 || box.sent[0].To[0] != "admin@example.test" || !strings.HasPrefix(box.sent[0].Subject, "mail.alert.subject") && box.sent[0].Subject == "" {
			t.Errorf("sent %+v", box.sent)
		}
		for _, h := range box.sent[0].Headers {
			if h.Name == "List-Unsubscribe" {
				t.Error("a sample can turn the admin's own alerts off")
			}
		}
	})
}

// The link in a notification turns it off only with a POST: opening it
// shows what it does, a mail client's one-click POST (without a CSRF
// token) or the page's button does it. A changed link does nothing.
func TestUnsubscribeLink(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := worldWith(t, db, map[string]string{"PANEL_PUBLIC_URL": "https://desk.example"})
		svc := w.Server.Deps().Mail
		ctx := context.Background()
		token, err := svc.UnsubscribeToken(ctx, mail.NotifyAlert, "ops@example.org")
		if err != nil {
			t.Fatal(err)
		}
		base := "http://" + w.Addr() + mail.UnsubscribePath + "?t=" + url.QueryEscape(token)
		get, err := http.Get(base)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(get.Body)
		get.Body.Close()
		if get.StatusCode != http.StatusOK || !strings.Contains(string(page), "<form method=\"post\"") || svc.Unsubscribed(ctx, mail.NotifyAlert, "ops@example.org") {
			t.Fatalf("open: %d %s", get.StatusCode, page)
		}
		bad, err := http.Post("http://"+w.Addr()+mail.UnsubscribePath+"?t="+url.QueryEscape(token+"x"), "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
		if err != nil {
			t.Fatal(err)
		}
		bad.Body.Close()
		if bad.StatusCode != http.StatusNotFound || svc.Unsubscribed(ctx, mail.NotifyAlert, "ops@example.org") {
			t.Errorf("a changed link: %d", bad.StatusCode)
		}
		post, err := http.Post(base, "application/x-www-form-urlencoded", strings.NewReader("List-Unsubscribe=One-Click"))
		if err != nil {
			t.Fatal(err)
		}
		post.Body.Close()
		if post.StatusCode != http.StatusOK || !svc.Unsubscribed(ctx, mail.NotifyAlert, "ops@example.org") {
			t.Fatalf("one-click: %d", post.StatusCode)
		}
		admin := w.As("admin")
		if res := admin.Do("GET", "/api/settings/email/unsubscribed", ""); !strings.Contains(res.Body, `"alert":["ops@example.org"]`) {
			t.Errorf("list: %s", res.Body)
		}
		if res := admin.Do("POST", "/api/settings/email/unsubscribed/resubscribe", `{"kind":"alert","address":"ops@example.org"}`, "Content-Type", "application/json"); res.Status != http.StatusOK || svc.Unsubscribed(ctx, mail.NotifyAlert, "ops@example.org") {
			t.Errorf("resubscribe: %d %s", res.Status, res.Body)
		}
	})
}
