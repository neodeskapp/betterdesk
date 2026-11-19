package mail

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

var configSeeds = []string{
	fullConfig, `{}`, ``, `null`, `[]`, `"text"`, `5`, `{"host":""}`, `{"host":"a","port":"2525","secure":"false","user":5,"pass":[1],"from":{"a":1},"tlsVerify":null}`,
	`{"host":5}`, `{"host":["127.0.0.1"],"port":70000}`, `{"host":{"toString":1}}`, `{"host":"a","port":{"toString":1}}`, `{"host":"a","port":25.5}`,
	`{"host":"a","port":1e400}`, `{"host":"a","port":-0}`, `{"host":"\ud800","user":"\udc00\ud83d","pass":"😀"}`, `{"host":"a","host":"b"}`,
	`{"2":1,"host":"a","1":2}`, "{\"host\":\"a\xff\"}", `{"host":"a","x":"\u0000\u001f "}`, ` {"host" : "a" } `, `{"host":"a",}`, `{"host":"a"}x`,
	`{"host":"a","from":["x@example.org",null,0,{"toString":1}]}`, `{"host":"a","user":"u","pass":{"toString":1}}`, strings.Repeat("[", 6000),
	`{"host":"a","alert_email":["  ops@example.org "]}`, `{"__proto__":{"host":"x"},"host":"y"}`, `{"host":"a","port":1E2,"x":[1.0,-0.0,1e-7,123456789012345678901234567890]}`,
}

// FuzzStoredConfig reads any text as the stored SMTP configuration, the
// way a hand-edited or damaged settings row would arrive.
func FuzzStoredConfig(f *testing.F) {
	for _, seed := range configSeeds {
		f.Add(seed)
	}
	service := New(Options{TLSVerify: true})
	f.Fuzz(func(t *testing.T, text string) {
		doc, err := parseJSON(text)
		// The grammar is JSON's: what encoding/json calls valid, and
		// nothing else. (Its own limit on nesting is far above anything
		// this short.)
		if len(text) < 4000 && (err == nil) != json.Valid([]byte(text)) {
			t.Fatalf("parseJSON(%q): %v, encoding/json says valid=%v", text, err, json.Valid([]byte(text)))
		}
		if err != nil {
			if configFromText(text) != nil {
				t.Fatalf("a configuration from %q, which is no JSON", text)
			}
			return
		}
		// What is written back is JSON, and writing it again changes nothing
		out := doc.json()
		if !json.Valid([]byte(out)) || !utf8.ValidString(out) {
			t.Fatalf("parseJSON(%q) writes %q", text, out)
		}
		again, err := parseJSON(out)
		if err != nil || again.json() != out {
			t.Fatalf("%q does not read back as itself: %v", out, err)
		}
		// The console's other reading of stored JSON agrees. It has no way
		// to keep half a surrogate pair, so those texts are left out.
		if !strings.Contains(out, `\ud`) && utf8.ValidString(text) && out != "null" {
			var other store.JSONParsed
			if err := other.Scan(text); err == nil {
				if theirs, err := other.MarshalJSON(); err == nil && string(theirs) != out {
					t.Fatalf("%q: %q here, %q in package store", text, out, theirs)
				}
			}
		}

		cfg := configFromText(text)
		if cfg == nil {
			if doc.member("host").truthy() {
				t.Fatalf("no configuration from %q, which has a host", text)
			}
			return
		}
		// The answer for the settings page never has the password
		masked, err := cfg.MarshalJSON()
		if err != nil || !json.Valid(masked) {
			t.Fatalf("%q answers %q %v", text, masked, err)
		}
		shown, err := parseJSON(string(masked))
		if err != nil || !shown.member("configured").flag {
			t.Fatalf("%q answers %q", text, masked)
		}
		if pass := shown.member("pass"); pass.kind != kindString || (pass.str != "" && pass.str != MaskedPass) || (pass.str == MaskedPass) != cfg.get("pass").truthy() {
			t.Fatalf("%q answers the password %q", text, pass.json())
		}
		if shown.member("secure").kind != kindBool || (shown.member("tlsVerify").kind != kindBool && shown.member("tlsVerify").kind != kindUndefined) {
			t.Fatalf("%q answers %q", text, masked)
		}

		// What the transport would be given
		tr := service.newTransporter(cfg)
		if want := cfg.get("secure").truthy(); tr.settings.Secure != want {
			t.Fatalf("%q: secure %v", text, tr.settings.Secure)
		}
		// A stored flag decides, anything else leaves the default (on here)
		wantVerify := true
		if v := cfg.get("tlsVerify"); v.kind == kindBool {
			wantVerify = v.flag
		}
		if tr.settings.TLSVerify != wantVerify {
			t.Fatalf("%q: certificate check %v, want %v", text, tr.settings.TLSVerify, wantVerify)
		}
		if tr.refusal != nil {
			if tr.refusal.Error() == "" {
				t.Fatalf("%q is refused without a reason", text)
			}
			return
		}
		st := tr.settings
		if st.Host == "" || st.Port < 1 || st.Port > 65535 || !utf8.ValidString(st.Host) || !utf8.ValidString(st.User) || !utf8.ValidString(st.Pass) {
			t.Fatalf("%q: settings %+v", text, st)
		}
		// Credentials go out only when the configuration has both
		if user, pass := cfg.get("user"), cfg.get("pass"); user.truthy() && pass.truthy() {
			userText, _ := user.toString()
			passText, _ := pass.toString()
			if st.User != wellFormed(userText) || st.Pass != wellFormed(passText) {
				t.Fatalf("%q: credentials %q %q", text, st.User, st.Pass)
			}
		} else if st.User != "" || st.Pass != "" {
			t.Fatalf("%q: credentials %q %q without both being set", text, st.User, st.Pass)
		}
		if cfg.get("host").kind != kindString {
			t.Fatalf("%q: a host that is no text went through", text)
		}
	})
}

var bodySeeds = []string{
	`{"host":"smtp.example.com","port":587,"secure":false,"user":"user","pass":"********","from":"from@example.com","alert_email":"ops@example.com"}`,
	`{}`, `[]`, `{"host":""}`, `{"host":" h ","port":"25abc","secure":"no","pass":"new","tlsVerify":0}`, `{"host":["a","b"],"port":[25],"alert_email":[" x "]}`,
	`{"host":{"toString":1}}`, `{"host":"h","port":{"toString":1}}`, `{"host":"h","alert_email":{"toString":1}}`, `{"host":"h","pass":null,"user":{"a":[1,2.50]},"from":0}`,
	`{"host":"h","port":1e400,"pass":"********","pass":"second"}`, `{"host":"h","port":"` + strings.Repeat("9", 400) + `"}`, `{"host":"\ud800 x  ","pass":"\udc00"}`,
	`{"host":5,"port":-0,"secure":[],"tlsVerify":null}`, `{"host":"h","pass":["********"]}`, `{"host":"h","pass":" ********"}`,
}

// FuzzSettingsBody reads any request body as the settings to save, over
// any stored row.
func FuzzSettingsBody(f *testing.F) {
	for i, seed := range bodySeeds {
		f.Add(seed, configSeeds[i%len(configSeeds)])
	}
	f.Fuzz(func(t *testing.T, raw, storedText string) {
		body := bodyOf(raw)
		existing, err := parseJSON(storedText)
		if err != nil {
			existing = undefined
		}
		host := bodyValue(body, "host")
		config, err := nextConfig(body, existing)
		if err != nil {
			if err != errNotPrimitive {
				t.Fatalf("%s: %v", raw, err)
			}
			return
		}
		out := config.json()
		if !json.Valid([]byte(out)) || !utf8.ValidString(out) {
			t.Fatalf("%s stores %q", raw, out)
		}
		if !host.truthy() {
			// The handler has answered 400 before it gets here
			return
		}
		// The members, in the order the console wrote them
		wantKeys := []string{"host", "port", "secure", "user", "pass", "from", "alert_email"}
		if body.Has("tlsVerify") {
			wantKeys = append(wantKeys, "tlsVerify")
		}
		if !sameStrings(config.keys, wantKeys) {
			t.Fatalf("%s stores the members %q", raw, config.keys)
		}
		stored, err := parseJSON(out)
		if err != nil {
			t.Fatalf("%s stores %q: %v", raw, out, err)
		}
		if h := stored.member("host"); h.kind != kindString || h.str != jsbind.Trim(h.str) {
			t.Fatalf("%s stores the host %s", raw, h.json())
		}
		if p := config.member("port"); p.kind != kindNumber || p.num == 0 || math.IsNaN(p.num) || (p.num != math.Trunc(p.num) && !math.IsInf(p.num, 0)) {
			t.Fatalf("%s stores the port %v", raw, p.num)
		}
		if stored.member("secure").kind != kindBool || stored.member("alert_email").kind != kindString {
			t.Fatalf("%s stores %q", raw, out)
		}
		if !stored.member("from").truthy() {
			t.Fatalf("%s stores no sender: %q", raw, out)
		}
		// The password: a new one when the request brings one, else the
		// one stored so far, else none. The mask is never taken for a
		// password.
		sent := bodyValue(body, "pass")
		kept := existing.member("pass")
		got := config.member("pass").json()
		switch {
		case sent.truthy() && !(sent.kind == kindString && sent.str == MaskedPass):
			if got != sent.json() {
				t.Fatalf("%s stores the password %s", raw, got)
			}
		case kept.truthy():
			if got != kept.json() {
				t.Fatalf("%s over %s stores the password %s", raw, storedText, got)
			}
		case got != `""`:
			t.Fatalf("%s over %s stores the password %s", raw, storedText, got)
		}
	})
}
