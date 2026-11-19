package serverinfo

import "strings"

// The recorded Node answers that the Go console gives otherwise on
// purpose: Node's answer was a bug that is fixed here. Each list names
// its row of BUGS.md.

// shortKeyMasks are the masked API keys of the recorded key files of 16
// characters or fewer, by the name of the case (SI4). Node showed a key
// of 8 characters or fewer whole and the first and last 4 characters of
// any longer key; Go shows at most a quarter of a short key at each end.
var shortKeyMasks = map[string]string{
	"placeholder":   "YOU****KEY",
	"short":         "ab****34",
	"nine":          "ab****45",
	"spaces inside": "abc****jkl",
	"non ascii":     "äöü****efg",
	"invalid utf8":  "ab****h\uFFFD",
	"nul":           "ab****ij",
}

// shortKeyCutMasks are the masked keys of the recorded keys that Node cut
// through a character, by the key (SI4). All of them have 16 characters
// or fewer, so a quarter at each end cuts through none of them.
var shortKeyCutMasks = map[string]string{
	"abc😀defghijk":  "abc****ijk",
	"abcdefghi😀jkl": "abc****jkl",
	"😀😀😀😀😀":         "😀****😀",
}

// fixedKeyRunAnswer is the answer of a recorded step of the key runs as
// the Go console gives it: the 8 character key of the runs masked (SI4).
func fixedKeyRunAnswer(node string) string {
	return strings.Replace(node, `"apiKeyMasked":"abcd1234"`, `"apiKeyMasked":"ab****34"`, 1)
}
