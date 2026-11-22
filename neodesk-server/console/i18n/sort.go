package i18n

// v8Sort sorts list in place with the algorithm of V8's
// Array.prototype.sort (TimSort), making the same comparisons in the same
// order. For a consistent order any stable sort gives the same result. The
// Accept-Language order is not consistent when a q value cannot be read
// (it compares equal to everything), and then the result depends on which
// pairs are compared.
//
// order(a, b) is negative when a sorts before b, positive when after, and
// zero when they are equal.
func v8Sort[T any](list []T, order func(a, b T) float64) {
	n := len(list)
	if n < 2 {
		return
	}
	s := &timSort[T]{a: list, order: order, minGallop: minGallopStart}
	low, remaining := 0, n
	minRun := remaining
	r := 0
	for minRun >= 64 {
		r |= minRun & 1
		minRun >>= 1
	}
	minRun += r
	for remaining != 0 {
		run := s.countRun(low, low+remaining)
		if run < minRun {
			forced := min(minRun, remaining)
			s.insertionSort(low, low+run, low+forced)
			run = forced
		}
		s.runs = append(s.runs, timRun{low, run})
		s.collapse()
		low += run
		remaining -= run
	}
	for len(s.runs) > 1 {
		i := len(s.runs) - 2
		if i > 0 && s.runs[i-1].length < s.runs[i+1].length {
			i--
		}
		s.mergeAt(i)
	}
}

const minGallopStart = 7

type timRun struct{ base, length int }

type timSort[T any] struct {
	a         []T
	order     func(a, b T) float64
	runs      []timRun
	minGallop int
}

// countRun finds the run that starts at low and makes it ascending.
func (s *timSort[T]) countRun(lowArg, high int) int {
	low := lowArg + 1
	if low == high {
		return 1
	}
	length := 2
	descending := s.order(s.a[low], s.a[low-1]) < 0
	previous := s.a[low]
	for i := low + 1; i < high; i++ {
		o := s.order(s.a[i], previous)
		if descending {
			if o >= 0 {
				break
			}
		} else if o < 0 {
			break
		}
		previous = s.a[i]
		length++
	}
	if descending {
		for i, j := lowArg, lowArg+length-1; i < j; i, j = i+1, j-1 {
			s.a[i], s.a[j] = s.a[j], s.a[i]
		}
	}
	return length
}

// insertionSort sorts [low, high) where [low, start) is already sorted.
func (s *timSort[T]) insertionSort(low, start, high int) {
	if start == low {
		start++
	}
	for ; start < high; start++ {
		left, right := low, start
		pivot := s.a[right]
		for left < right {
			mid := left + (right-left)>>1
			if s.order(pivot, s.a[mid]) < 0 {
				right = mid
			} else {
				left = mid + 1
			}
		}
		for p := start; p > left; p-- {
			s.a[p] = s.a[p-1]
		}
		s.a[left] = pivot
	}
}

func (s *timSort[T]) invariant(n int) bool {
	if n < 2 {
		return true
	}
	return s.runs[n-2].length > s.runs[n-1].length+s.runs[n].length
}

func (s *timSort[T]) collapse() {
	for len(s.runs) > 1 {
		n := len(s.runs) - 2
		if !s.invariant(n+1) || !s.invariant(n) {
			if n > 0 && s.runs[n-1].length < s.runs[n+1].length {
				n--
			}
			s.mergeAt(n)
		} else if s.runs[n].length <= s.runs[n+1].length {
			s.mergeAt(n)
		} else {
			break
		}
	}
}

func (s *timSort[T]) mergeAt(i int) {
	baseA, lengthA := s.runs[i].base, s.runs[i].length
	baseB, lengthB := s.runs[i+1].base, s.runs[i+1].length
	s.runs[i].length = lengthA + lengthB
	if i == len(s.runs)-3 {
		s.runs[i+1] = s.runs[i+2]
	}
	s.runs = s.runs[:len(s.runs)-1]

	// Where does B start in A? Elements of A before that are in place.
	k := s.gallopRight(s.a, s.a[baseB], baseA, lengthA, 0)
	baseA += k
	lengthA -= k
	if lengthA == 0 {
		return
	}
	// Where does A end in B? Elements of B after that are in place.
	lengthB = s.gallopLeft(s.a, s.a[baseA+lengthA-1], baseB, lengthB, lengthB-1)
	if lengthB == 0 {
		return
	}
	if lengthA <= lengthB {
		s.mergeLow(baseA, lengthA, baseB, lengthB)
	} else {
		s.mergeHigh(baseA, lengthA, baseB, lengthB)
	}
}

// gallopLeft returns the offset k in the sorted run arr[base:base+length]
// with arr[base+k-1] < key <= arr[base+k], searching outward from hint.
func (s *timSort[T]) gallopLeft(arr []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if s.order(arr[base+hint], key) < 0 {
		maxOfs := length - hint
		for offset < maxOfs && s.order(arr[base+hint+offset], key) < 0 {
			lastOfs = offset
			offset = offset<<1 + 1
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs += hint
		offset += hint
	} else {
		maxOfs := hint + 1
		for offset < maxOfs && s.order(arr[base+hint-offset], key) >= 0 {
			lastOfs = offset
			offset = offset<<1 + 1
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs, offset = hint-offset, hint-lastOfs
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + (offset-lastOfs)>>1
		if s.order(arr[base+m], key) < 0 {
			lastOfs = m + 1
		} else {
			offset = m
		}
	}
	return offset
}

// gallopRight returns the offset k with arr[base+k-1] <= key < arr[base+k].
func (s *timSort[T]) gallopRight(arr []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if s.order(key, arr[base+hint]) < 0 {
		maxOfs := hint + 1
		for offset < maxOfs && s.order(key, arr[base+hint-offset]) < 0 {
			lastOfs = offset
			offset = offset<<1 + 1
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs, offset = hint-offset, hint-lastOfs
	} else {
		maxOfs := length - hint
		for offset < maxOfs && s.order(key, arr[base+hint+offset]) >= 0 {
			lastOfs = offset
			offset = offset<<1 + 1
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs += hint
		offset += hint
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + (offset-lastOfs)>>1
		if s.order(key, arr[base+m]) < 0 {
			offset = m
		} else {
			lastOfs = m + 1
		}
	}
	return offset
}

// mergeLow merges two adjacent runs, A the shorter, from the left.
func (s *timSort[T]) mergeLow(baseA, lengthA, baseB, lengthB int) {
	tmp := append([]T(nil), s.a[baseA:baseA+lengthA]...)
	dest, cursorTmp, cursorB := baseA, 0, baseB

	s.a[dest] = s.a[cursorB]
	dest++
	cursorB++
	lengthB--
	succeed := func() {
		if lengthA > 0 {
			copy(s.a[dest:dest+lengthA], tmp[cursorTmp:cursorTmp+lengthA])
		}
	}
	copyB := func() {
		copy(s.a[dest:dest+lengthB], s.a[cursorB:cursorB+lengthB])
		s.a[dest+lengthB] = tmp[cursorTmp]
	}
	if lengthB == 0 {
		succeed()
		return
	}
	if lengthA == 1 {
		copyB()
		return
	}
	minGallop := s.minGallop
	for {
		wonA, wonB := 0, 0
		for {
			if s.order(s.a[cursorB], tmp[cursorTmp]) < 0 {
				s.a[dest] = s.a[cursorB]
				dest++
				cursorB++
				wonB++
				lengthB--
				wonA = 0
				if lengthB == 0 {
					succeed()
					return
				}
				if wonB >= minGallop {
					break
				}
			} else {
				s.a[dest] = tmp[cursorTmp]
				dest++
				cursorTmp++
				wonA++
				lengthA--
				wonB = 0
				if lengthA == 1 {
					copyB()
					return
				}
				if wonA >= minGallop {
					break
				}
			}
		}
		minGallop++
		for first := true; first || wonA >= minGallopStart || wonB >= minGallopStart; first = false {
			if minGallop > 1 {
				minGallop--
			}
			s.minGallop = minGallop

			wonA = s.gallopRight(tmp, s.a[cursorB], cursorTmp, lengthA, 0)
			if wonA > 0 {
				copy(s.a[dest:dest+wonA], tmp[cursorTmp:cursorTmp+wonA])
				dest += wonA
				cursorTmp += wonA
				lengthA -= wonA
				if lengthA == 1 {
					copyB()
					return
				}
				if lengthA == 0 {
					succeed()
					return
				}
			}
			s.a[dest] = s.a[cursorB]
			dest++
			cursorB++
			lengthB--
			if lengthB == 0 {
				succeed()
				return
			}

			wonB = s.gallopLeft(s.a, tmp[cursorTmp], cursorB, lengthB, 0)
			if wonB > 0 {
				copy(s.a[dest:dest+wonB], s.a[cursorB:cursorB+wonB])
				dest += wonB
				cursorB += wonB
				lengthB -= wonB
				if lengthB == 0 {
					succeed()
					return
				}
			}
			s.a[dest] = tmp[cursorTmp]
			dest++
			cursorTmp++
			lengthA--
			if lengthA == 1 {
				copyB()
				return
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}

// mergeHigh merges two adjacent runs, B the shorter, from the right.
func (s *timSort[T]) mergeHigh(baseA, lengthA, baseB, lengthB int) {
	tmp := append([]T(nil), s.a[baseB:baseB+lengthB]...)
	dest := baseB + lengthB - 1
	cursorTmp := lengthB - 1
	cursorA := baseA + lengthA - 1

	s.a[dest] = s.a[cursorA]
	dest--
	cursorA--
	lengthA--
	succeed := func() {
		if lengthB > 0 {
			copy(s.a[dest-(lengthB-1):dest+1], tmp[:lengthB])
		}
	}
	copyA := func() {
		dest -= lengthA
		cursorA -= lengthA
		copy(s.a[dest+1:dest+1+lengthA], s.a[cursorA+1:cursorA+1+lengthA])
		s.a[dest] = tmp[cursorTmp]
	}
	if lengthA == 0 {
		succeed()
		return
	}
	if lengthB == 1 {
		copyA()
		return
	}
	minGallop := s.minGallop
	for {
		wonA, wonB := 0, 0
		for {
			if s.order(tmp[cursorTmp], s.a[cursorA]) < 0 {
				s.a[dest] = s.a[cursorA]
				dest--
				cursorA--
				wonA++
				lengthA--
				wonB = 0
				if lengthA == 0 {
					succeed()
					return
				}
				if wonA >= minGallop {
					break
				}
			} else {
				s.a[dest] = tmp[cursorTmp]
				dest--
				cursorTmp--
				wonB++
				lengthB--
				wonA = 0
				if lengthB == 1 {
					copyA()
					return
				}
				if wonB >= minGallop {
					break
				}
			}
		}
		minGallop++
		for first := true; first || wonA >= minGallopStart || wonB >= minGallopStart; first = false {
			if minGallop > 1 {
				minGallop--
			}
			s.minGallop = minGallop

			k := s.gallopRight(s.a, tmp[cursorTmp], baseA, lengthA, lengthA-1)
			wonA = lengthA - k
			if wonA > 0 {
				dest -= wonA
				cursorA -= wonA
				copy(s.a[dest+1:dest+1+wonA], s.a[cursorA+1:cursorA+1+wonA])
				lengthA -= wonA
				if lengthA == 0 {
					succeed()
					return
				}
			}
			s.a[dest] = tmp[cursorTmp]
			dest--
			cursorTmp--
			lengthB--
			if lengthB == 1 {
				copyA()
				return
			}

			k = s.gallopLeft(tmp, s.a[cursorA], 0, lengthB, lengthB-1)
			wonB = lengthB - k
			if wonB > 0 {
				dest -= wonB
				cursorTmp -= wonB
				copy(s.a[dest+1:dest+1+wonB], tmp[cursorTmp+1:cursorTmp+1+wonB])
				lengthB -= wonB
				if lengthB == 1 {
					copyA()
					return
				}
				if lengthB == 0 {
					succeed()
					return
				}
			}
			s.a[dest] = s.a[cursorA]
			dest--
			cursorA--
			lengthA--
			if lengthA == 0 {
				succeed()
				return
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}
