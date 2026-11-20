package cli

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/managedconfig"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

type fileState struct {
	Base64 string `json:"base64"`
	Mode   int    `json:"mode"`
	Dir    bool   `json:"dir"`
}

type envMergeCase struct {
	Name       string                `json:"name"`
	Template   string                `json:"template"`
	Target     *string               `json:"target"`
	Files      map[string]string     `json:"files"`
	Args       []string              `json:"args"`
	Env        map[string]string     `json:"env"`
	SubstEnv   bool                  `json:"substEnv"`
	Status     int                   `json:"status"`
	Stdout     string                `json:"stdout"`
	Stderr     string                `json:"stderr"`
	StderrKind string                `json:"stderrKind"`
	After      map[string]*fileState `json:"after"`
}

// testEnv is an Env with the given variables and captured streams.
func testEnv(vars map[string]string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
		Getenv: func(k string) string { return vars[k] }, GOOS: runtime.GOOS,
	}, &out, &errOut
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// compareFiles checks the files a run left against what Node left.
func compareFiles(t *testing.T, name, dir string, want map[string]*fileState, root ...string) {
	t.Helper()
	for file, state := range want {
		full := filepath.Join(dir, file)
		info, err := os.Stat(full)
		if state == nil {
			if err == nil {
				t.Errorf("%s: %s exists, Node left none", name, file)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %s: %v", name, file, err)
			continue
		}
		if state.Dir != info.IsDir() {
			t.Errorf("%s: %s is a folder: %v, in Node %v", name, file, info.IsDir(), state.Dir)
			continue
		}
		if state.Dir {
			continue
		}
		got, _ := os.ReadFile(full)
		for _, r := range root {
			got = bytes.ReplaceAll(got, []byte(r), []byte("{ROOT}"))
		}
		if wantBytes := mustDecode(t, state.Base64); !bytes.Equal(got, wantBytes) {
			t.Errorf("%s: %s\n got %q\nwant %q", name, file, clipText(string(got)), clipText(string(wantBytes)))
		}
		if runtime.GOOS != "windows" && int(info.Mode().Perm()) != state.Mode {
			t.Errorf("%s: %s mode %o, Node %o", name, file, info.Mode().Perm(), state.Mode)
		}
	}
}

func clipText(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// Every recorded run of merge-env.js (and of write-installer-env-subst.js
// before it, replayed as --subst-env) gives the same exit status, the
// same JSON and the same files, byte for byte and with the same mode.
func TestEnvMergeLikeNode(t *testing.T) {
	var recorded struct {
		Templates map[string]string `json:"templates"`
		Cases     []envMergeCase    `json:"cases"`
	}
	readVectors(t, "envmerge", &recorded)
	if len(recorded.Cases) < 300 {
		t.Fatalf("only %d cases", len(recorded.Cases))
	}
	kinds := map[string]int{}
	for _, c := range recorded.Cases {
		dir := t.TempDir()
		target := filepath.Join(dir, ".env")
		switch {
		case c.Target == nil:
		case *c.Target == "DIRECTORY":
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
		default:
			if err := os.WriteFile(target, mustDecode(t, *c.Target), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for file, content := range c.Files {
			if err := os.WriteFile(filepath.Join(dir, file), mustDecode(t, content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var args []string
		for i := 0; i < len(c.Args); i++ {
			if c.SubstEnv && c.Args[i] == "--subst-file" {
				args = append(args, "--subst-env")
				i++
				continue
			}
			args = append(args, strings.ReplaceAll(c.Args[i], "{DIR}", dir))
		}
		env, out, errOut := testEnv(c.Env)
		status := envMerge(args, env, string(mustDecode(t, recorded.Templates[c.Template])), serverinfo.OSFS{})
		kinds[c.StderrKind]++

		if status != c.Status {
			t.Errorf("%s: exit %d, Node %d (stderr %q)", c.Name, status, c.Status, errOut.String())
		}
		if got := strings.ReplaceAll(out.String(), dir, "{DIR}"); got != c.Stdout {
			t.Errorf("%s: stdout\n got %q\nwant %q", c.Name, got, c.Stdout)
		}
		gotErr := strings.ReplaceAll(errOut.String(), dir, "{DIR}")
		switch c.StderrKind {
		case "json", "":
			if gotErr != c.Stderr {
				t.Errorf("%s: stderr\n got %q\nwant %q", c.Name, gotErr, c.Stderr)
			}
		case "usage":
			if !strings.HasPrefix(gotErr, "Usage: betterdesk-server env merge") {
				t.Errorf("%s: stderr %q, want the usage", c.Name, gotErr)
			}
		case "uncaught":
			// Node printed the stack of the exception
			if gotErr == "" || strings.HasPrefix(gotErr, "{") {
				t.Errorf("%s: stderr %q, want a message", c.Name, gotErr)
			}
		}
		after := c.After
		if sum, fixed := envMergeDifferences[c.Name]; fixed {
			after = goEnvAfter(t, c, dir, sum)
		}
		compareFiles(t, c.Name, dir, after)
	}
	t.Logf("%d runs replayed (stderr: %v), %d with the .env of SI6", len(recorded.Cases), kinds, len(envMergeDifferences))
}

type substFileCase struct {
	Text    string      `json:"text"`
	Entries [][3]string `json:"entries"`
	Error   string      `json:"error"`
}

// A substitution file gives the members Object.assign(subs,
// JSON.parse(text)) gave, in the order Object.entries lists them, with
// the text String(v ?? ”) and String(v) made of each.
func TestSubstFilesLikeNode(t *testing.T) {
	var cases []substFileCase
	readVectors(t, "substfiles", &cases)
	if len(cases) < 1000 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		subs := newObject[substValue]()
		err := readSubstFile(subs, []byte(c.Text))
		if (err != nil) != (c.Error != "") {
			t.Errorf("%q: error %v, Node %q", c.Text, err, c.Error)
			continue
		}
		if err != nil {
			continue
		}
		var got [][3]string
		for _, key := range subs.entries() {
			v := subs.values[key]
			checked := v.text
			if v.null {
				checked = "null"
			}
			got = append(got, [3]string{key, base64.StdEncoding.EncodeToString([]byte(v.text)), base64.StdEncoding.EncodeToString([]byte(checked))})
		}
		if !slices.Equal(got, c.Entries) {
			t.Errorf("%q:\n got %q\nwant %q", c.Text, got, c.Entries)
		}
	}
}

// The command uses the built-in template, console/managedconfig/env.example.
func TestEnvMergeUsesTheBuiltInTemplate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".env")
	env, out, errOut := testEnv(map[string]string{"BD_SUBST_RUSTDESK_DIR": "/srv/rd"})
	if status := EnvMerge([]string{"--target", target, "--fresh", "--subst-env"}, env); status != 0 {
		t.Fatalf("exit %d: %s", status, errOut)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	template := managedconfig.Template()
	if !strings.Contains(template, "__RUSTDESK_DIR__") || !strings.Contains(string(got), "/srv/rd") || strings.Contains(string(got), "__RUSTDESK_DIR__") {
		t.Errorf("the template was not filled: %s", clipText(string(got)))
	}
	if !strings.HasPrefix(out.String(), `{"success":true,"mode":"fresh","added":["`) {
		t.Errorf("stdout %q", out.String())
	}
}
