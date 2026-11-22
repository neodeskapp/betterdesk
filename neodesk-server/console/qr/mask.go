package qr

// A mask flips some of the data modules so the code has no large plain
// areas and nothing that looks like a finder pattern. There are eight; the
// format information says which one was used.

// maskAt reports whether a mask flips the module at row, col (ISO/IEC
// 18004:2015, 7.8.2, table 10).
func maskAt(mask, row, col int) bool {
	switch mask {
	case 0:
		return (row+col)%2 == 0
	case 1:
		return row%2 == 0
	case 2:
		return col%3 == 0
	case 3:
		return (row+col)%3 == 0
	case 4:
		return (row/2+col/3)%2 == 0
	case 5:
		return row*col%2+row*col%3 == 0
	case 6:
		return (row*col%2+row*col%3)%2 == 0
	default:
		return (row*col%3+(row+col)%2)%2 == 0
	}
}

// applyMask flips the data modules a mask selects. Applying the same mask
// again restores them.
func (m *matrix) applyMask(mask int) {
	for row := 0; row < m.size; row++ {
		for col := 0; col < m.size; col++ {
			at := row*m.size + col
			if !m.function[at] && maskAt(mask, row, col) {
				m.dark[at] = !m.dark[at]
			}
		}
	}
}

// bestMask tries all eight masks on the finished code, each with its own
// format information in place, and returns the one with the lowest
// penalty. Of masks with the same penalty the lowest number wins. The
// matrix is left unmasked.
func (m *matrix) bestMask(level Level) int {
	best, lowest := 0, -1
	for mask := 0; mask < 8; mask++ {
		m.drawFormat(level, mask)
		m.applyMask(mask)
		penalty := m.penalty()
		m.applyMask(mask)
		if lowest < 0 || penalty < lowest {
			best, lowest = mask, penalty
		}
	}
	return best
}

// penalty scores how hard a masked code is to read; lower is better. It is
// the sum of four rules (ISO/IEC 18004:2015, 7.8.3, table 11).
func (m *matrix) penalty() int {
	return m.penaltyRuns() + m.penaltyBlocks() + m.penaltyFinderLike() + m.penaltyBalance()
}

// penaltyRuns is rule 1: every run of five or more modules of one colour in
// a row or a column costs 3, plus 1 for each module beyond five.
func (m *matrix) penaltyRuns() int {
	points := 0
	for line := 0; line < m.size; line++ {
		// Walk row "line" and column "line" at the same time.
		rowRun, colRun := 0, 0
		var rowLast, colLast bool
		for i := 0; i < m.size; i++ {
			if d := m.dark[line*m.size+i]; i > 0 && d == rowLast {
				rowRun++
			} else {
				points += runPenalty(rowRun)
				rowLast, rowRun = d, 1
			}
			if d := m.dark[i*m.size+line]; i > 0 && d == colLast {
				colRun++
			} else {
				points += runPenalty(colRun)
				colLast, colRun = d, 1
			}
		}
		points += runPenalty(rowRun) + runPenalty(colRun)
	}
	return points
}

func runPenalty(length int) int {
	if length < 5 {
		return 0
	}
	return 3 + (length - 5)
}

// penaltyBlocks is rule 2: every 2x2 square of one colour costs 3. Squares
// may overlap, so a 3x3 area of one colour counts four times.
func (m *matrix) penaltyBlocks() int {
	blocks := 0
	for row := 0; row < m.size-1; row++ {
		for col := 0; col < m.size-1; col++ {
			at := row*m.size + col
			d := m.dark[at]
			if m.dark[at+1] == d && m.dark[at+m.size] == d && m.dark[at+m.size+1] == d {
				blocks++
			}
		}
	}
	return blocks * 3
}

// penaltyFinderLike is rule 3: the sequence dark, light, three dark, light,
// dark (the cross-section of a finder pattern) with four light modules
// before or after it costs 40 each time it shows up in a row or a column.
func (m *matrix) penaltyFinderLike() int {
	const (
		lightAfter  = 0b10111010000
		lightBefore = 0b00001011101
	)
	found := 0
	for line := 0; line < m.size; line++ {
		// The last eleven modules of the row and of the column, as bits.
		rowBits, colBits := 0, 0
		for i := 0; i < m.size; i++ {
			rowBits = rowBits << 1 & 0x7FF
			if m.dark[line*m.size+i] {
				rowBits |= 1
			}
			if i >= 10 && (rowBits == lightAfter || rowBits == lightBefore) {
				found++
			}
			colBits = colBits << 1 & 0x7FF
			if m.dark[i*m.size+line] {
				colBits |= 1
			}
			if i >= 10 && (colBits == lightAfter || colBits == lightBefore) {
				found++
			}
		}
	}
	return found * 40
}

// penaltyBalance is rule 4: the further the share of dark modules is from
// one half, the higher the cost. The share in percent is divided by five
// and rounded up; every step that result is away from 10 costs 10. A code
// that is 43 % dark gives 9 and costs 10; one that is 58 % dark gives 12
// and costs 20.
func (m *matrix) penaltyBalance() int {
	dark := 0
	for _, d := range m.dark {
		if d {
			dark++
		}
	}
	total := len(m.dark)
	steps := (dark*20+total-1)/total - 10
	if steps < 0 {
		steps = -steps
	}
	return steps * 10
}
