package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// Any env file: upgrade-env keeps every line it does not change byte for
// byte and in order, comments out or takes out only the keys of its
// lists, adds only what is missing, leaves the loader's value of every
// other key alone, and a second run changes nothing.
func FuzzUpgradeEnv(f *testing.F) {
	for _, seed := range []string{"", "PORT=5000\n", "A=1\r\nGOCACHE=x\r\n", byteOrderMark + "HOST=0.0.0.0", "PORT=99999",
		"  NODE_EXTRA_CA_CERTS = x\n#PORT=1\n=x\nWEB_UI_DIR\nWEB_UI_DIR=\n", "CONSOLE_PORT=0\nPORT=abc\n", "\r\n\r\n", "A=\xff\xfe\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		plan, err := planUpgrade(text, "/srv/c/.env", "systemd")
		if err != nil {
			if !errors.Is(err, errNotAPort) {
				t.Fatalf("%q: %v", text, err)
			}
			if _, ok := loadLikeTheServer(strings.TrimPrefix(text, byteOrderMark))["CONSOLE_PORT"]; ok {
				t.Fatalf("%q: refused PORT although CONSOLE_PORT is there", text)
			}
			return
		}
		out := plan.Text
		if strings.HasPrefix(text, byteOrderMark) {
			if !strings.HasPrefix(out, byteOrderMark) {
				t.Fatalf("%q: the byte order mark is gone", text)
			}
			text, out = text[len(byteOrderMark):], out[len(byteOrderMark):]
		}
		// Walk the input lines and find each in the output
		for rest := text; rest != ""; {
			line := rest
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				line, rest = rest[:i+1], rest[i+1:]
			} else {
				rest = ""
			}
			// As the loader cuts lines: a last line keeps a "\r" of its own
			content := line
			if strings.HasSuffix(line, "\n") {
				content = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			}
			entry := serverinfo.ParseEnvLines(content).Entries[0]
			_, comment := commentedKeys[entry.Key]
			_, remove := removedKeys[entry.Key]
			switch {
			case entry.Type == serverinfo.EnvPair && remove:
				continue
			case entry.Type == serverinfo.EnvPair && comment:
				ending := line[len(content):]
				noteEnd := ending
				if noteEnd == "" {
					noteEnd = "\n"
					if strings.Contains(text, "\r\n") {
						noteEnd = "\r\n"
					}
				}
				want := "# Commented out by neodesk-server upgrade-env: " + commentedKeys[entry.Key] + noteEnd + "# " + content + ending
				if !strings.HasPrefix(out, want) {
					t.Fatalf("%q: %q is not commented out at %q", text, content, out)
				}
				out = out[len(want):]
			default:
				if !strings.HasPrefix(out, line) {
					t.Fatalf("%q: %q is not kept at %q", text, line, out)
				}
				out = out[len(line):]
			}
		}
		// What is left is the added block
		if len(plan.Added) == 0 && out != "" {
			t.Fatalf("%q: %q added without a key", text, out)
		}
		for _, s := range plan.Added {
			if !strings.Contains(out, s.Key+"="+s.Value) {
				t.Fatalf("%q: %s is not in %q", text, s.Key, out)
			}
		}
		before, after := loadLikeTheServer(text), loadLikeTheServer(plan.Text)
		for key, value := range before {
			_, comment := commentedKeys[key]
			_, remove := removedKeys[key]
			if !comment && !remove && after[key] != value {
				t.Fatalf("%q: %s is %q now, was %q", text, key, after[key], value)
			}
			if (comment || remove) && after[key] != "" {
				t.Fatalf("%q: %s is still set", text, key)
			}
		}
		for _, key := range []string{"HOST", "SIGNAL_PORT", "RELAY_PORT", "GO_API_PORT", "CONSOLE_DIR", "NEODESK_ENV_FILE", "NEODESK_SUPERVISOR"} {
			if _, ok := serverinfo.ParseEnvLines(plan.Text).Get(key); !ok {
				t.Fatalf("%q: no %s after the upgrade", text, key)
			}
		}
		// CONSOLE_PORT comes from a port PORT names; without one the
		// server's default applies (DECISIONS A250)
		if _, ok := serverinfo.ParseEnvLines(plan.Text).Get("CONSOLE_PORT"); !ok && plan.FromPort != "" {
			t.Fatalf("%q: CONSOLE_PORT %s was not written", text, plan.FromPort)
		}
		again, err := planUpgrade(plan.Text, "/srv/c/.env", "systemd")
		if err != nil || again.Changed() || again.Text != plan.Text {
			t.Fatalf("%q: a second run: %v %+v", text, err, again)
		}
	})
}

// Any substitution file: no panic, the same entries every time, each key
// once, array indexes first in their order, never "__proto__", and a
// value only text.
func FuzzSubstFile(f *testing.F) {
	for _, seed := range []string{`{"A":"1"}`, `{"2":1,"1":[null,[2]],"b":{},"__proto__":"x"}`, `["a",1e400]`, `"\ud83d"`, `{"A":`, "\xef\xbb\xbf{}", `{"a":"\u0000"}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		first := newObject[substValue]()
		err := readSubstFile(first, raw)
		second := newObject[substValue]()
		if err2 := readSubstFile(second, raw); (err == nil) != (err2 == nil) {
			t.Fatalf("%q: %v then %v", raw, err, err2)
		}
		if err != nil {
			if json.Valid([]byte(serverinfo.DecodeUTF8(raw))) {
				t.Fatalf("%q: refused valid JSON: %v", raw, err)
			}
			return
		}
		keys := first.entries()
		if strings.Join(keys, "\x00") != strings.Join(second.entries(), "\x00") {
			t.Fatalf("%q: two orders", raw)
		}
		seen := map[string]bool{}
		lastIndex, sawName := -1, false
		for _, key := range keys {
			if seen[key] || key == "__proto__" {
				t.Fatalf("%q: key %q", raw, key)
			}
			seen[key] = true
			if n, ok := arrayIndex(key); ok {
				if sawName || int(n) <= lastIndex {
					t.Fatalf("%q: index %q out of order in %q", raw, key, keys)
				}
				lastIndex = int(n)
			} else {
				sawName = true
			}
			v := first.values[key]
			if v.null && v.text != "" {
				t.Fatalf("%q: null with text", raw)
			}
		}
	})
}

// Any target and substitution file through the whole command: no panic,
// an exit status of the script's, and on success an env file that keeps
// the target's text in a merge.
func FuzzEnvMerge(f *testing.F) {
	f.Add([]byte("A=1\n"), []byte(`{"A":"x"}`), false)
	f.Add([]byte(""), []byte(`{"RUSTDESK_DIR":"__X__"}`), true)
	f.Add([]byte("  \n"), []byte(`[1]`), false)
	f.Add([]byte("B=\xff\r\n"), []byte(`{`), false)
	template := "# T\nA=__A__\nB=2\n\nC=__RUSTDESK_DIR__\n"
	f.Fuzz(func(t *testing.T, target, subst []byte, fresh bool) {
		dir := t.TempDir()
		file := filepath.Join(dir, ".env")
		if err := os.WriteFile(file, target, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "s.json"), subst, 0o644); err != nil {
			t.Fatal(err)
		}
		args := []string{"--target", file, "--subst-file", filepath.Join(dir, "s.json")}
		if fresh {
			args = append(args, "--fresh")
		}
		env, out, _ := testEnv(nil)
		status := envMerge(args, env, template, serverinfo.OSFS{})
		switch status {
		case 0:
			var result struct {
				Success bool   `json:"success"`
				Mode    string `json:"mode"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Success {
				t.Fatalf("stdout %q: %v", out.String(), err)
			}
			written, _ := os.ReadFile(file)
			if result.Mode == "merge" && !strings.HasPrefix(serverinfo.DecodeUTF8(written), serverinfo.DecodeUTF8(target)) {
				t.Fatalf("a merge lost the target's text: %q -> %q", target, written)
			}
		case 1:
		default:
			t.Fatalf("exit %d", status)
		}
	})
}
