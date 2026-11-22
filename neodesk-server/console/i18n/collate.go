package i18n

import (
	"sort"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// sortByBaseLetters sorts list by keys the way JavaScript's
// String.prototype.localeCompare with sensitivity "base" orders them in an
// English locale: case and accents do not count, and equal keys keep their
// order.
func sortByBaseLetters(list []*Object, keys []string) {
	c := collate.New(language.English, collate.Loose)
	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return c.CompareString(keys[idx[a]], keys[idx[b]]) < 0
	})
	sorted := make([]*Object, len(list))
	for i, from := range idx {
		sorted[i] = list[from]
	}
	copy(list, sorted)
}
