package goapi

import "unicode"

// localeCompare orders two texts the way the device list has always been
// ordered: by JavaScript's a.localeCompare(b) in an English locale. It
// returns a negative number, zero or a positive number.
//
// The order has three levels. First the letters and digits decide, without
// regard to case or accents: white space sorts before punctuation, then
// symbols, digits and letters. Texts that are the same on that level are
// ordered by their accents, and texts that still are the same by case,
// lower case first. Control characters do not count.
//
// ASCII and the Latin letters with accents (Latin-1 and Latin Extended-A:
// the letters of the western, central and northern European languages and
// of Turkish) follow that order. Two things differ from JavaScript. The l
// with a middle dot sorts as an l with an accent, not as an l and a dot.
// And every other character (other scripts, other punctuation) sorts after
// the Latin letters, by code point.
func localeCompare(a, b string) int {
	ka, kb := collationKey(a), collationKey(b)
	for level := 0; level < 3; level++ {
		if c := compareWeights(ka[level], kb[level]); c != 0 {
			return c
		}
	}
	return 0
}

func compareWeights(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// asciiOrder lists the ASCII characters that count, lowest first. Letters
// follow the digits.
const asciiOrder = "\t\n\v\f\r _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789"

// letterStep leaves room between two letters of the alphabet for a letter
// that sorts between them, such as the dotless i.
const letterStep = 2

func letterPrimary(c byte) int { return 100 + letterStep*int(c-'a') }

var asciiPrimary = func() [128]int {
	var w [128]int
	for i := 0; i < len(asciiOrder); i++ {
		w[asciiOrder[i]] = i + 1
	}
	for c := byte('a'); c <= 'z'; c++ {
		w[c] = letterPrimary(c)
		w[c-'a'+'A'] = letterPrimary(c)
	}
	return w
}()

// Accent weights, lowest first: a letter without an accent sorts before the
// same letter with one, and é before è before ê.
const (
	noAccent = iota
	acute
	grave
	breve
	circumflex
	caron
	ring
	diaeresis
	doubleAcute
	tilde
	dotAbove
	cedilla
	commaBelow
	stroke
	ogonek
	macron
	middleDot
	eth
	longS
	ligature
)

// accentedLetters lists, for each accent, the lower-case letters that carry
// it, each after its base letter: "aá" is á, an a with that accent.
var accentedLetters = []struct {
	accent int
	pairs  string
}{
	{acute, "aácćeéiílĺnńoórŕsśuúyýzź"},
	{grave, "aàeèiìoòuù"},
	{breve, "aăeĕgğiĭoŏuŭ"},
	{circumflex, "aâcĉeêgĝhĥiîjĵoôsŝuûwŵyŷ"},
	{caron, "cčdďeělľnňrřsštťzž"},
	{ring, "aåuů"},
	{diaeresis, "aäeëiïoöuüyÿ"},
	{doubleAcute, "oőuű"},
	{tilde, "aãiĩnñoõuũ"},
	{dotAbove, "cċeėgġzż"},
	{cedilla, "cçgģkķlļnņrŗsştţ"},
	{commaBelow, "sștț"},
	{stroke, "dđhħlłoø"},
	{ogonek, "aąeęiįuų"},
	{macron, "aāeēiīoōuū"},
	{middleDot, "lŀ"},
	{eth, "dð"},
}

type accentedLetter struct {
	base   byte
	accent int
}

var accented = func() map[rune]accentedLetter {
	m := map[rune]accentedLetter{}
	for _, group := range accentedLetters {
		runes := []rune(group.pairs)
		for i := 0; i+1 < len(runes); i += 2 {
			m[runes[i+1]] = accentedLetter{base: byte(runes[i]), accent: group.accent}
		}
	}
	return m
}()

// ligatures are the letters that sort as two: æ as "ae" with a mark.
var ligatures = map[rune]string{'æ': "ae", 'œ': "oe", 'ĳ': "ij", 'ß': "ss"}

// ownLetters are the letters that are not a variant of an ASCII letter. Each
// sorts directly after the letter named here: the dotless i after i, the
// thorn after z.
var ownLetters = map[rune]byte{'ı': 'i', 'ĸ': 'q', 'ŋ': 'n', 'ŧ': 't', 'þ': 'z'}

// collationKey returns the weights of s on the three levels.
func collationKey(s string) [3][]int {
	var key [3][]int
	add := func(primary, secondary, tertiary int) {
		key[0] = append(key[0], primary)
		key[1] = append(key[1], secondary)
		key[2] = append(key[2], tertiary)
	}
	for _, r := range s {
		upper := 0
		if unicode.IsUpper(r) {
			upper = 1
		}
		if r < 128 {
			if w := asciiPrimary[r]; w != 0 {
				add(w, noAccent, upper)
			}
			continue
		}
		switch r {
		case 'İ':
			add(letterPrimary('i'), dotAbove, 1)
			continue
		case 'ſ':
			// The long s sorts after every other s, accented ones included.
			add(letterPrimary('s'), longS, 0)
			continue
		case 'ŉ':
			// An n after an apostrophe that is a letter of its own, after z.
			add(letterPrimary('z')+letterStep, noAccent, 0)
			add(letterPrimary('n'), noAccent, 0)
			continue
		}
		lower := unicode.ToLower(r)
		if letter, ok := accented[lower]; ok {
			add(letterPrimary(letter.base), letter.accent, upper)
			continue
		}
		if pair, ok := ligatures[lower]; ok {
			add(letterPrimary(pair[0]), ligature, upper)
			add(letterPrimary(pair[1]), noAccent, upper)
			continue
		}
		if after, ok := ownLetters[lower]; ok {
			add(letterPrimary(after)+1, noAccent, upper)
			continue
		}
		add(1000+int(lower), noAccent, upper)
	}
	return key
}
