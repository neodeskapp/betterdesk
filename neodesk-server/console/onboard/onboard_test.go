package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

func TestCodeForms(t *testing.T) {
	for _, in := range []string{"ABCD2345", "abcd-2345", " AB CD 23 45 ", "abcd2345"} {
		if !ValidFormat(in) || Normalize(in) != "ABCD2345" || Display(in) != "ABCD-2345" {
			t.Errorf("%q: valid %v, normalized %q, shown %q", in, ValidFormat(in), Normalize(in), Display(in))
		}
	}
	for _, in := range []string{"", "ABCD234", "ABCD23456", "ABCD-2O45", "ABCD-1345", "ABCD-I345", "ABC$2345"} {
		if ValidFormat(in) {
			t.Errorf("%q passes as a code", in)
		}
	}
}

func TestConsoleLinksAndFileNames(t *testing.T) {
	for _, tc := range []struct {
		base, join, support, deep, setup, quick string
	}{
		{"https://desk.example.com", "https://desk.example.com/join/ABCD2345", "https://desk.example.com/s/ABCD2345",
			"neodesk://join/desk.example.com/ABCD2345",
			"NeoDesk-Setup-desk.example.com-ABCD2345.exe", "NeoDesk-QuickSupport-desk.example.com-ABCD2345.exe"},
		{"https://Desk.Example.com:443/", "https://desk.example.com/join/ABCD2345", "https://desk.example.com/s/ABCD2345",
			"neodesk://join/desk.example.com/ABCD2345",
			"NeoDesk-Setup-desk.example.com-ABCD2345.exe", "NeoDesk-QuickSupport-desk.example.com-ABCD2345.exe"},
		{"https://desk.example.com:8443", "https://desk.example.com:8443/join/ABCD2345", "https://desk.example.com:8443/s/ABCD2345",
			"neodesk://join/desk.example.com:8443/ABCD2345",
			"NeoDesk-Setup-desk.example.com_8443-ABCD2345.exe", "NeoDesk-QuickSupport-desk.example.com_8443-ABCD2345.exe"},
		{"http://192.168.1.5:5000", "http://192.168.1.5:5000/join/ABCD2345", "http://192.168.1.5:5000/s/ABCD2345",
			"neodesk://join/192.168.1.5:5000/ABCD2345?http=1",
			"NeoDesk-Setup-192.168.1.5_5000_http-ABCD2345.exe", "NeoDesk-QuickSupport-192.168.1.5_5000_http-ABCD2345.exe"},
		{"http://desk.lan", "http://desk.lan/join/ABCD2345", "http://desk.lan/s/ABCD2345",
			"neodesk://join/desk.lan/ABCD2345?http=1",
			"NeoDesk-Setup-desk.lan_http-ABCD2345.exe", "NeoDesk-QuickSupport-desk.lan_http-ABCD2345.exe"},
		{"https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443/join/ABCD2345", "https://[2001:db8::1]:8443/s/ABCD2345",
			"neodesk://join/[2001:db8::1]:8443/ABCD2345",
			"NeoDesk-Setup-ABCD2345.exe", "NeoDesk-QuickSupport-ABCD2345.exe"},
	} {
		c, ok := ParseConsole(tc.base)
		if !ok {
			t.Fatalf("%s does not parse", tc.base)
		}
		got := []string{c.JoinLink("abcd-2345"), c.SupportLink("abcd-2345"), c.DeepLink(KindJoin, "abcd-2345"),
			c.FileName(KindJoin, "abcd-2345", ".exe"), c.FileName(KindSupport, "abcd-2345", ".exe")}
		want := []string{tc.join, tc.support, tc.deep, tc.setup, tc.quick}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: got %q, want %q", tc.base, got[i], want[i])
			}
		}
	}
	if c, _ := ParseConsole("https://desk.example.com"); c.DeepLink(KindSupport, "ABCD2345") != "neodesk://support/desk.example.com/ABCD2345" {
		t.Errorf("support deep link %q", c.DeepLink(KindSupport, "ABCD2345"))
	}
}

func TestConsoleRefusesHostsAShellOrFileNameCannotCarry(t *testing.T) {
	for _, base := range []string{"https://a'b.example.com", "https://a$(x).example", "ftp://desk.example.com", "https://", "desk.example.com", "https://a_b.example.com", "https://desk.example.com:99999"} {
		if _, ok := ParseConsole(base); ok {
			t.Errorf("%q parses", base)
		}
	}
}

func TestScriptsCarryTheirLinkAndCode(t *testing.T) {
	install := InstallScript("https://desk.example.com", "ABCD2345")
	for _, want := range []string{
		"BASE='https://desk.example.com'", "CODE='ABCD2345'", `neodesk --join "$BASE/join/$CODE"`,
		`/api/onboard/public/$CODE/download?os=linux&arch=$arch&format=$format`, "apt-get install -y --reinstall --allow-downgrades", `"$tool" install -y`,
	} {
		if !strings.Contains(install, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	run := RunScript("https://desk.example.com", "ABCD2345")
	if !strings.Contains(run, `exec "$dir/neodesk" --join "$BASE/s/$CODE"`) || strings.Contains(run, "sudo") || strings.Contains(run, "@BASE@") {
		t.Errorf("run script:\n%s", run)
	}
}

func TestMailEscapesAndCarriesTheLink(t *testing.T) {
	tr := func(key string, vars map[string]any) string {
		out := key
		for _, k := range []string{"server", "sender"} {
			if v, ok := vars[k]; ok {
				out += " " + k + "=" + v.(string)
			}
		}
		return out
	}
	b := brandmail.Branding{Server: "<b>Acme</b>", Host: "desk.example.com"}
	m := BuildMail(tr, MailFacts{Kind: KindSupport, Link: "https://desk.example.com/s/ABCD-2345", Code: "ABCD2345", Branding: b, Sender: "Ana"})
	if !strings.HasPrefix(m.Subject, "mail.support.subject") {
		t.Errorf("subject %q", m.Subject)
	}
	built := brandmail.Render(m, b, "")
	if !strings.Contains(built.Text, "https://desk.example.com/s/ABCD-2345") || !strings.Contains(built.HTML, `href="https://desk.example.com/s/ABCD-2345"`) {
		t.Error("the link is missing")
	}
	if strings.Contains(built.HTML, "<b>Acme</b>") || !strings.Contains(built.HTML, "&lt;b&gt;Acme&lt;/b&gt;") {
		t.Errorf("the server name is not escaped: %s", built.HTML)
	}
	if strings.Contains(built.Text, "mail.invite.code_lead") {
		t.Errorf("support mail text: %s", built.Text)
	}
	j := BuildMail(tr, MailFacts{Kind: KindJoin, Code: "ABCD2345", Branding: b})
	if !strings.HasPrefix(j.Subject, "mail.invite.subject") || j.CodeLead != "mail.invite.code_lead server=<b>Acme</b> sender=" || j.Code != "ABCD-2345" {
		t.Errorf("invite mail: %q %q %q", j.Subject, j.CodeLead, j.Code)
	}
}

// sessionStore stands in for the API server's session records.
type sessionStore struct {
	mu   sync.Mutex
	rows map[string]string
	puts int
}

func (f *sessionStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimPrefix(r.URL.Path, "/api/onboard/sessions/")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/onboard/sessions":
		list := []json.RawMessage{}
		for _, v := range f.rows {
			list = append(list, json.RawMessage(v))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "sessions": list})
	case r.Method == http.MethodPut:
		data, _ := io.ReadAll(r.Body)
		f.rows[id] = string(data)
		f.puts++
		_, _ = io.WriteString(w, `{"success":true}`)
	case r.Method == http.MethodDelete:
		delete(f.rows, id)
		_, _ = io.WriteString(w, `{"success":true}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newSessions(t *testing.T) (*Sessions, *sessionStore, *clock) {
	t.Helper()
	store := &sessionStore{rows: map[string]string{}}
	c := &clock{now: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	return NewSessions(goapi.New(store), c.Now), store, c
}

func supportCode(id string, by int64, now time.Time) Code {
	return Code{ID: id, Kind: KindSupport, CreatedBy: "ana", CreatedByID: by,
		CreatedAt: stamp(now), ExpiresAt: stamp(now.Add(SupportLifetime)), MaxUses: 1, UsesLeft: 1}
}

func TestSessionTokenOpensOnlyItsOwnOpenSession(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	tokenA, a, err := s.Redeem(ctx, supportCode("aaaaaaaaaaaaaaaa", 5, c.now))
	if err != nil {
		t.Fatal(err)
	}
	tokenB, _, err := s.Redeem(ctx, supportCode("bbbbbbbbbbbbbbbb", 5, c.now))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ByToken(ctx, tokenA); err != nil || got.ID != a.ID {
		t.Fatalf("token A: %+v %v", got, err)
	}
	if got, err := s.ByToken(ctx, tokenB); err != nil || got.ID != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("token B: %+v %v", got, err)
	}
	if _, err := s.ByToken(ctx, strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown token: %v", err)
	}
	if _, err := s.End(ctx, a.ID, EndedByCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByToken(ctx, tokenA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the token of an ended session still works: %v", err)
	}
	if _, _, err := s.Redeem(ctx, supportCode("aaaaaaaaaaaaaaaa", 5, c.now)); !errors.Is(err, ErrState) {
		t.Fatalf("an ended session opened again: %v", err)
	}
}

func TestSessionStepsGoInOrder(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	_, sess, err := s.Redeem(ctx, supportCode("cccccccccccccccc", 5, c.now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consent(ctx, sess.ID, true, nil); !errors.Is(err, ErrState) {
		t.Fatalf("consent before a device: %v", err)
	}
	if err := s.SetPassword(ctx, sess.ID, "pw"); !errors.Is(err, ErrState) {
		t.Fatalf("a password before consent: %v", err)
	}
	if sess, err = s.SetDevice(ctx, sess.ID, Device{DeviceID: "123456789", Hostname: "laptop"}, true, true); err != nil || sess.Status != StatusAsking {
		t.Fatalf("device: %+v %v", sess, err)
	}
	if _, err := s.SetDevice(ctx, sess.ID, Device{DeviceID: "223456789"}, true, true); !errors.Is(err, ErrState) {
		t.Fatalf("a second device: %v", err)
	}
	if sess, err = s.Consent(ctx, sess.ID, true, nil); err != nil || sess.Status != StatusAllowed {
		t.Fatalf("consent: %+v %v", sess, err)
	}
	if err := s.SetPassword(ctx, sess.ID, "pw"); err != nil {
		t.Fatal(err)
	}
	if !s.HasPassword(sess.ID) {
		t.Fatal("no password waits")
	}
}

func TestDeclineEndsTheSession(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	token, sess, _ := s.Redeem(ctx, supportCode("dddddddddddddddd", 5, c.now))
	_, _ = s.SetDevice(ctx, sess.ID, Device{DeviceID: "123456789"}, false, false)
	sess, err := s.Consent(ctx, sess.ID, false, nil)
	if err != nil || sess.Status != StatusDeclined || sess.EndedBy != EndedByCustomer || sess.EndedAt == "" {
		t.Fatalf("decline: %+v %v", sess, err)
	}
	if _, err := s.ByToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("the token of a declined session still works")
	}
}

func TestPasswordGoesOnceToTheSupporterWhoMadeTheSession(t *testing.T) {
	ctx := context.Background()
	s, store, c := newSessions(t)
	_, sess, _ := s.Redeem(ctx, supportCode("eeeeeeeeeeeeeeee", 5, c.now))
	_, _ = s.SetDevice(ctx, sess.ID, Device{DeviceID: "123456789"}, true, true)
	_, _ = s.Consent(ctx, sess.ID, true, nil)
	if err := s.SetPassword(ctx, sess.ID, "s3cret-pw"); err != nil {
		t.Fatal(err)
	}
	for _, row := range store.rows {
		if strings.Contains(row, "s3cret-pw") {
			t.Fatal("the password reached the store")
		}
	}
	if _, err := s.TakePassword(ctx, sess.ID, 6); !errors.Is(err, ErrNotYours) {
		t.Fatalf("another account took the password: %v", err)
	}
	if _, err := s.TakePasswordForDevice(ctx, "123456789", 6); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("another account took it by device: %v", err)
	}
	if pw, err := s.TakePassword(ctx, sess.ID, 5); err != nil || pw != "s3cret-pw" {
		t.Fatalf("take: %q %v", pw, err)
	}
	if _, err := s.TakePassword(ctx, sess.ID, 5); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("taken twice: %v", err)
	}
	_ = s.SetPassword(ctx, sess.ID, "next-pw")
	if pw, err := s.TakePasswordForDevice(ctx, "123456789", 5); err != nil || pw != "next-pw" {
		t.Fatalf("take by device: %q %v", pw, err)
	}
	_ = s.SetPassword(ctx, sess.ID, "last-pw")
	if _, err := s.End(ctx, sess.ID, EndedBySupporter); err != nil {
		t.Fatal(err)
	}
	if s.HasPassword(sess.ID) {
		t.Fatal("the password outlived the session")
	}
}

func TestSweepEndsWhatTimeEnds(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	t0 := c.now
	waiting, _ := s.Create(ctx, supportCode("1111111111111111", 5, c.now))
	_, idle, _ := s.Redeem(ctx, supportCode("2222222222222222", 5, c.now))
	idle = mustDevice(t, s, idle.ID, true)
	tokenLive, live, _ := s.Redeem(ctx, supportCode("3333333333333333", 5, c.now))

	c.now = t0.Add(IdleLimit - time.Second)
	if _, err := s.ByToken(ctx, tokenLive); err != nil {
		t.Fatal(err)
	}
	c.now = t0.Add(IdleLimit + time.Second)
	due, err := s.Sweep(ctx)
	if err != nil || len(due) != 0 {
		t.Fatalf("sweep 1: %v %v", due, err)
	}
	if got, _ := s.Get(ctx, idle.ID); got.Status != StatusEnded || got.EndedBy != EndedByTimeout {
		t.Fatalf("the idle session: %+v", got)
	}
	if got, _ := s.Get(ctx, live.ID); got.Status != StatusOpened {
		t.Fatalf("the session that called in ended: %+v", got)
	}
	if got, _ := s.Get(ctx, waiting.ID); got.Status != StatusWaiting {
		t.Fatalf("the waiting session ended early: %+v", got)
	}

	c.now = c.now.Add(DeviceRemoveAfter - time.Second)
	if due, _ = s.Sweep(ctx); len(due) != 0 {
		t.Fatalf("a device is due too early: %v", due)
	}
	c.now = c.now.Add(2 * time.Second)
	due, _ = s.Sweep(ctx)
	if len(due) != 1 || due[0].ID != idle.ID {
		t.Fatalf("due devices: %+v", due)
	}
	if err := s.DeviceRemoved(ctx, idle.ID); err != nil {
		t.Fatal(err)
	}
	if due, _ = s.Sweep(ctx); len(due) != 0 {
		t.Fatalf("a removed device is due again: %v", due)
	}

	c.now = t0.Add(SupportLifetime - time.Second)
	_, _ = s.Sweep(ctx)
	if got, _ := s.Get(ctx, waiting.ID); got.Status != StatusWaiting {
		t.Fatalf("the link expired early: %+v", got)
	}
	c.now = t0.Add(SupportLifetime)
	_, _ = s.Sweep(ctx)
	if got, _ := s.Get(ctx, waiting.ID); got.Status != StatusExpired || got.EndedBy != EndedByTimeout {
		t.Fatalf("the expired link: %+v", got)
	}
}

func mustDevice(t *testing.T, s *Sessions, id string, temporary bool) Session {
	t.Helper()
	sess, err := s.SetDevice(context.Background(), id, Device{DeviceID: "923456789"}, temporary, false)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestSessionsOutliveARestartAndOldOnesArePruned(t *testing.T) {
	ctx := context.Background()
	s, store, c := newSessions(t)
	token, sess, _ := s.Redeem(ctx, supportCode("4444444444444444", 5, c.now))
	again := NewSessions(goapi.New(store), c.Now)
	if got, err := again.ByToken(ctx, token); err != nil || got.ID != sess.ID {
		t.Fatalf("after a restart: %+v %v", got, err)
	}
	if _, err := again.End(ctx, sess.ID, EndedBySupporter); err != nil {
		t.Fatal(err)
	}
	c.now = c.now.Add(91 * 24 * time.Hour)
	if err := again.Prune(ctx, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("records left: %v", store.rows)
	}
}

func TestOpenOfListsTheAccountsOpenSessions(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	_, _ = s.Create(ctx, supportCode("5555555555555555", 5, c.now))
	_, _ = s.Create(ctx, supportCode("6666666666666666", 6, c.now))
	ended, _ := s.Create(ctx, supportCode("7777777777777777", 5, c.now))
	_, _ = s.End(ctx, ended.ID, EndedBySupporter)
	list, err := s.OpenOf(ctx, 5)
	if err != nil || len(list) != 1 || list[0].ID != "5555555555555555" {
		t.Fatalf("open of 5: %+v %v", list, err)
	}
}

func TestADeviceUsedAgainStaysForTheNextSession(t *testing.T) {
	ctx := context.Background()
	s, _, c := newSessions(t)
	_, first, _ := s.Redeem(ctx, supportCode("8888888888888888", 5, c.now))
	first = mustDevice(t, s, first.ID, true)
	if _, err := s.End(ctx, first.ID, EndedByCustomer); err != nil {
		t.Fatal(err)
	}
	c.now = c.now.Add(time.Minute)
	_, second, _ := s.Redeem(ctx, supportCode("9999999999999999", 5, c.now))
	second = mustDevice(t, s, second.ID, true)

	c.now = c.now.Add(DeviceRemoveAfter)
	due, err := s.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.DeviceID == first.DeviceID {
			t.Fatalf("the device of the open session %s is due for removal by %s", second.ID, d.ID)
		}
	}
	if got, _ := s.Get(ctx, first.ID); !got.DeviceRemoved {
		t.Fatal("the first session still holds the device")
	}
}
