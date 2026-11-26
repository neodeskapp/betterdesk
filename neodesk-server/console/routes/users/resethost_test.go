package users_test

import (
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
)

// "Forgot password?" on the sign-in page needs the console's address from
// its settings (PANEL_PUBLIC_URL, PUBLIC_URL or the panel's public host).
// Before (review of 26 Nov 2026) it took the request's host when none was
// set, so anyone could ask for a reset link for someone else with a host
// of their own and the mail led the person, with a working link, there.
// Without an address the page offers no reset and a request sends nothing.
func TestForgotPasswordNeedsTheConsolesAddress(t *testing.T) {
	w, box := mailWorld(t, true)
	admin := w.As("admin")
	makeAccount(t, w, admin, "hilde", "hilde@example.test")
	w.Server.Deps().Mail = mail.New(mail.Options{DB: w.DB, Transport: box, Getenv: func(string) string { return "" }})

	person := w.SignedOut()
	expect(t, "available", person.Do("GET", "/api/auth/password-reset/available", ""), 200, `{"success":true,"data":{"available":false}}`)
	expect(t, "request", person.Do("POST", "/api/auth/password-reset/request", `{"login":"hilde"}`,
		"X-Forwarded-Host", "desk.evil.test", "X-Forwarded-Proto", "https"), 200, `{"success":true}`)
	time.Sleep(300 * time.Millisecond)
	if sent := box.mailTo("hilde@example.test"); len(sent) > 0 {
		t.Fatalf("a reset mail went out without the console's address: %q", *sent[0].Text)
	}
}
