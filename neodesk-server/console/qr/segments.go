package qr

import "strings"

// mode is the way a part of the text is written into the code. The values
// are the four-bit mode indicators (ISO/IEC 18004:2015, table 2).
type mode uint8

const (
	numeric      mode = 0b0001 // digits, three in 10 bits
	alphanumeric mode = 0b0010 // the 45 characters of alphanumericChars, two in 11 bits
	byteMode     mode = 0b0100 // anything, one byte in 8 bits
)

// alphanumericChars is the alphanumeric set in the order of its values 0 to
// 44 (ISO/IEC 18004:2015, table 5).
const alphanumericChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

// maxChars is the longest text any code holds: version 40 at level L filled
// with digits (ISO/IEC 18004:2015, table 7). A longer text fits nowhere,
// whatever it is made of.
const maxChars = 7089

// segment is a part of the text and the mode it is written in.
type segment struct {
	mode mode
	data string
}

// charCountBits is the width of the field that holds a segment's length. It
// grows with the version (ISO/IEC 18004:2015, table 3).
func charCountBits(m mode, version int) int {
	group := 2
	switch {
	case version < 10:
		group = 0
	case version < 27:
		group = 1
	}
	switch m {
	case numeric:
		return [3]int{10, 12, 14}[group]
	case alphanumeric:
		return [3]int{9, 11, 13}[group]
	default:
		return [3]int{8, 16, 16}[group]
	}
}

// dataBits is the number of bits n characters take in a mode, without the
// mode indicator and the length field (ISO/IEC 18004:2015, 7.4.3 to 7.4.5).
func dataBits(m mode, n int) int {
	switch m {
	case numeric:
		// Three digits in 10 bits; a last group of one or two digits takes
		// 4 or 7 bits.
		bits := 10 * (n / 3)
		if rest := n % 3; rest > 0 {
			bits += rest*3 + 1
		}
		return bits
	case alphanumeric:
		// Two characters in 11 bits; a last single character takes 6.
		return 11*(n/2) + 6*(n%2)
	default:
		return 8 * n
	}
}

// classOf is the tightest mode a byte can be written in. Bytes of UTF-8
// sequences and bytes that are not valid UTF-8 are plain bytes.
func classOf(b byte) mode {
	switch {
	case b >= '0' && b <= '9':
		return numeric
	case strings.IndexByte(alphanumericChars, b) >= 0:
		return alphanumeric
	default:
		return byteMode
	}
}

// splitRuns cuts the text into the longest runs of one class: digits, other
// alphanumeric characters, everything else. "AB12cd" becomes "AB", "12",
// "cd". Letters and digits are separate runs even though both could be
// written as alphanumeric; joining them is the job of optimize.
func splitRuns(text string) []segment {
	var runs []segment
	start := 0
	for i := 1; i <= len(text); i++ {
		if i == len(text) || classOf(text[i]) != classOf(text[start]) {
			runs = append(runs, segment{mode: classOf(text[start]), data: text[start:i]})
			start = i
		}
	}
	return runs
}

// plan picks the segments and the version for a text.
//
// The length field of a segment depends on the version, and the version
// depends on the segments. So the version is first estimated from the plain
// runs, the segments are optimised with the field widths of that estimate,
// and the final version is the smallest one that holds those segments.
func plan(text string, level Level) ([]segment, int, error) {
	if len(text) > maxChars {
		return nil, 0, ErrTooLong
	}
	runs := splitRuns(text)
	estimate, ok := fitVersion(runs, level)
	if !ok {
		estimate = 40
	}
	segments := optimize(text, runs, estimate)
	version, ok := fitVersion(segments, level)
	if !ok {
		return nil, 0, ErrTooLong
	}
	return segments, version, nil
}

// choice is one way to write one run: in its own mode or in a wider one.
type choice struct {
	mode   mode
	length int // characters in the run; every character is one byte
}

// edge leads to a choice of the next run and costs the bits that choice adds.
type edge struct {
	to   int
	bits int
}

// widerModes lists the modes a run of a class can be written in, tightest
// first.
func widerModes(class mode) []mode {
	switch class {
	case numeric:
		return []mode{numeric, alphanumeric, byteMode}
	case alphanumeric:
		return []mode{alphanumeric, byteMode}
	default:
		return []mode{byteMode}
	}
}

// optimize decides for every run whether it is written in its own mode or
// joined with its neighbours in a wider one, so that the whole text takes
// few bits. Starting a segment costs a mode indicator and a length field,
// so "A1B2" is cheaper as one alphanumeric segment than as four segments.
//
// It is a shortest path search. Every run has up to three choices (its own
// mode and the wider ones). An edge leads from each choice of one run to
// each choice of the next and costs the bits the next run adds: its data
// bits plus a new segment header when the mode changes, or only the extra
// data bits when the mode stays.
//
// The cost of staying in a mode does not look at how long the segment
// already is. It is worked out as if the segment so far were empty when
// the mode is the run's own, and as if it were as long as the run when the
// mode is a wider one. Digits and alphanumeric characters share groups of
// bits, so for them the result can be one bit off the true cost. This is
// how the npm package qrcode counts; the search keeps it, and keeps the
// package's order of visiting choices, so that both cut every text the
// same way.
func optimize(text string, runs []segment, version int) []segment {
	const start = 0
	choices := []choice{{}} // index 0 stands for the start
	edges := [][]edge{nil}
	previous := []int{start}

	for _, run := range runs {
		n := len(run.data)
		var current []int
		for i, m := range widerModes(run.mode) {
			id := len(choices)
			choices = append(choices, choice{mode: m, length: n})
			edges = append(edges, nil)
			current = append(current, id)

			// What a segment that goes on in mode m is taken to hold so far.
			before := 0
			if i > 0 {
				before = n
			}
			for _, p := range previous {
				var bits int
				if p != start && choices[p].mode == m {
					bits = dataBits(m, before+n) - dataBits(m, before)
				} else {
					bits = dataBits(m, n) + 4 + charCountBits(m, version)
				}
				edges[p] = append(edges[p], edge{to: id, bits: bits})
			}
		}
		previous = current
	}

	end := len(choices)
	for _, p := range previous {
		edges[p] = append(edges[p], edge{to: end, bits: 0})
	}
	edges = append(edges, nil)

	// The path has one choice per run, in the order of the text. Runs that
	// follow each other in the same mode become one segment.
	path := shortestPath(edges, start, end)
	var segments []segment
	var current mode
	from, to := 0, 0
	for _, id := range path[1 : len(path)-1] {
		c := choices[id]
		if to > from && c.mode != current {
			segments = append(segments, segment{mode: current, data: text[from:to]})
			from = to
		}
		current = c.mode
		to += c.length
	}
	return append(segments, segment{mode: current, data: text[from:to]})
}

// shortestPath is Dijkstra's search from start to end over edges, where
// edges[n] lists the edges that leave node n. It returns the nodes of the
// cheapest path, start and end included.
//
// When two paths cost the same, the result depends on the order of work,
// which is fixed here: the waiting nodes are kept sorted by cost, a node
// goes behind the ones that wait with the same cost, the edges of a node
// are tried in their listed order, and a node gets a new predecessor only
// for a strictly lower cost.
func shortestPath(edges [][]edge, start, end int) []int {
	type waiting struct{ node, cost int }

	const unknown = -1
	cost := make([]int, len(edges))
	previous := make([]int, len(edges))
	for i := range cost {
		cost[i] = unknown
		previous[i] = unknown
	}
	cost[start] = 0

	queue := []waiting{{node: start, cost: 0}}
	for len(queue) > 0 {
		nearest := queue[0]
		queue = queue[1:]
		for _, e := range edges[nearest.node] {
			c := nearest.cost + e.bits
			if cost[e.to] != unknown && cost[e.to] <= c {
				continue
			}
			cost[e.to] = c
			previous[e.to] = nearest.node

			at := len(queue)
			for at > 0 && queue[at-1].cost > c {
				at--
			}
			queue = append(queue, waiting{})
			copy(queue[at+1:], queue[at:])
			queue[at] = waiting{node: e.to, cost: c}
		}
	}

	var path []int
	for n := end; n != unknown; n = previous[n] {
		path = append(path, n)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}
