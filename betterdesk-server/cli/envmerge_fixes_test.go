package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// envMergeDifferences are the recorded runs of merge-env.js whose .env
// Go writes otherwise, by the run's name, with the SHA-256 of the .env Go
// writes. Node carried the comment and blank lines above template keys
// the file has already along to the next key it added; Go writes only
// those right above an added key (BUGS SI6). Node's recording stays as it
// is: Go's .env must also be Node's with nothing but such lines left out.
var envMergeDifferences = map[string]string{
	"real/some/merge":                  "04d3b7f2455a8c8ba9bd9534240e2fb753c7a022582fd491fc0e090aeb8063b7",
	"real/crlf/merge":                  "6624553014afe942383d431b9616c6902fa49fbdb8193297ffa88ef0bb0feeb5",
	"small/some/merge":                 "a79ddc659267e2733be293d7965ac845ebd8439105e70ec185fa3a78e3a17f46",
	"small/crlf/merge":                 "e233c784689f57dc8e238a9f1a72343cd9b1e3b186efad48d9cae0df4c3054f8",
	"crlf/some/merge":                  "aaa4d21c4068a4e6ab9dbe9a711025eaf714065d3b44a64ff902f69967e27f2c",
	"crlf/crlf/merge":                  "6be7366948fc09efdc9c2691c64b1b6dd60c4ead799b8188c78579971e778fd1",
	"crlf/notrail/merge":               "119698ab1feb2fe967b407e80ef9543702dbb0e5c1f1af5e5e8c500bdbd9d96a",
	"crlf/dup/merge":                   "7655a82690251be15b2abfee8fe4845daa524fe5feb60a3ddac838f087796de9",
	"crlf/brokenutf8/merge":            "0cb2be7aabedce0801633e64c67942fe1336f3c8798f42452ef7b128cc8422b9",
	"crlf/bom/merge":                   "e5407827f5ddd9414ec660e99c9e4336325b6624c63bd037de7deb1228724b3e",
	"subst/installer/small/some":       "a79ddc659267e2733be293d7965ac845ebd8439105e70ec185fa3a78e3a17f46",
	"subst/special/small/some":         "9fc14e6b4d8eea606efbd581c1566da0a546bf6e488e3b95d10b2432f98c2f7a",
	"subst/types/small/some":           "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/intkeys/small/some":         "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/dupkeys/small/some":         "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/proto/small/some":           "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/protoobj/small/some":        "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/array/small/some":           "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/string/small/some":          "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/astral/small/some":          "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/number/small/some":          "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/null/small/some":            "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/bool/small/some":            "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/unicode/small/some":         "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/emptykey/small/some":        "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/lone/small/some":            "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/deep/small/some":            "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/brokenutf8/small/some":      "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/whitespace/small/some":      "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"subst/numbers/small/some":         "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/7/small":                     "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/8/small":                     "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/9/small":                     "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/10/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/11/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/12/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/13/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/14/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/15/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/16/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/25/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/26/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/27/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/32/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"args/33/small":                    "573e3d921cc07538c5dde46768da24fd807ebb7d91188970c9e35f86c26d1d16",
	"env/all/real/some/merge":          "04d3b7f2455a8c8ba9bd9534240e2fb753c7a022582fd491fc0e090aeb8063b7",
	"env/all/small/some/merge":         "a79ddc659267e2733be293d7965ac845ebd8439105e70ec185fa3a78e3a17f46",
	"env/none/real/some/merge":         "955e87169ba66043a4b1dc03bb14bf3f91ab5705108c6785333e6315918424a0",
	"env/none/small/some/merge":        "558daff91e9fdd51fa66ee2355b87b304dca7a40a0cea4d65661594beea30321",
	"env/some/real/some/merge":         "11af8a2f84acfcefa567880c94a3a07a583c2c97d154f231f9123a17b9141c09",
	"env/some/small/some/merge":        "7c61eaa1c5719c45dee48dd1a521023f81f067fbe729696abcd81bf11c569e2d",
	"env/special/real/some/merge":      "1cbed25585c0e7d3a002faa7e727255943f53718941d6ac4916d71e7b7c8a793",
	"env/special/small/some/merge":     "558daff91e9fdd51fa66ee2355b87b304dca7a40a0cea4d65661594beea30321",
	"env/placeholder/real/some/merge":  "efab96e1d9aa5f6ca1d17293b89d62defb5fe7f8791b7259708b56f16f80dd66",
	"env/placeholder/small/some/merge": "2d4023ff1b7a7267f9ec448e29f78a5b99bf567983a7da340dd83c825f8e8eeb",
	"env/empty/real/some/merge":        "8f3f43f8aefec1d226a8090ecb850aa055125bbce44af89cdf1b44c2aa3cc7bb",
	"env/empty/small/some/merge":       "558daff91e9fdd51fa66ee2355b87b304dca7a40a0cea4d65661594beea30321",
}

// goEnvAfter checks the .env a run listed in envMergeDifferences left and
// returns the files to compare with Node's, with Go's .env in place of
// Node's.
func goEnvAfter(t *testing.T, c envMergeCase, dir, wantSum string) map[string]*fileState {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Errorf("%s: %v", c.Name, err)
		return c.After
	}
	node := c.After[".env"]
	if node == nil {
		t.Errorf("%s: listed for SI6, but Node wrote no .env", c.Name)
		return c.After
	}
	if !withoutCarriedComments(string(mustDecode(t, node.Base64)), string(got)) {
		t.Errorf("%s: the .env differs from Node's in more than the comments Node carried along\n got %q\nNode %q",
			c.Name, clipText(string(got)), clipText(string(mustDecode(t, node.Base64))))
	}
	if sum := sha256.Sum256(got); hex.EncodeToString(sum[:]) != wantSum {
		t.Errorf("%s: the .env has SHA-256 %x, recorded %s", c.Name, sum, wantSum)
	}
	after := map[string]*fileState{}
	for name, state := range c.After {
		after[name] = state
	}
	after[".env"] = &fileState{Base64: base64.StdEncoding.EncodeToString(got), Mode: node.Mode}
	return after
}

// withoutCarriedComments reports whether got is Node's merge result with
// nothing but lines that are no KEY=value pair left out (SI6). Node
// carried the comment and blank lines above template keys the file has
// along to the next key it added; Go writes only those right above an
// added key.
func withoutCarriedComments(node, got string) bool {
	gotLines := strings.Split(got, "\n")
	j := 0
	for _, line := range strings.Split(node, "\n") {
		if j < len(gotLines) && line == gotLines[j] {
			j++
			continue
		}
		if entries := serverinfo.ParseEnvLines(line).Entries; len(entries) > 0 && entries[0].Type == serverinfo.EnvPair {
			return false
		}
	}
	return j == len(gotLines)
}
