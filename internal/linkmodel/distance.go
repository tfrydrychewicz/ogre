package linkmodel

// This file depends on nothing else in the package: strings and ints only.

// distanceStackRows is the room Distance keeps on the stack. It covers two
// names of up to 64 bytes that share no prefix and no suffix, which is every
// realistic link name, so the common call allocates nothing.
const distanceStackRows = 3 * 65

// Distance returns the number of edits that turn a into b, or maxEdits+1 as
// soon as that number must exceed maxEdits. In other words it returns
// min(distance, maxEdits+1), and the caller tests "Distance(...) <= maxEdits".
//
// The distance is Damerau-Levenshtein in its optimal-string-alignment form
// (PRD 5.6): an insertion, a deletion, a substitution, or a swap of two
// adjacent characters each count as one edit, and no character is edited
// twice. So "reprots" is one edit from "reports", while "ca" is three edits
// from "abc" (the unrestricted Damerau-Levenshtein distance would say two).
// Every character counts alike: "oncall" and "on_call" are each one edit from
// "on-call".
//
// The bound is what makes scanning every name of a large Directory cheap: only
// the cells within maxEdits of the diagonal are computed, and the work stops at
// the first row that already costs more than maxEdits. With the small bounds
// suggestions use (0 to 2), a name that has nothing in common with the
// requested one is dismissed after three or four characters.
//
// Strings are compared byte by byte. Link names are ASCII (PRD 5.2), where a
// byte is a character; other input is safe and simply yields the distance
// between the byte sequences. Case is not folded: callers pass lowercase names.
// A negative maxEdits means 0, so the result is then 0 for equal strings and 1
// for all others.
func Distance(a, b string, maxEdits int) int {
	var stack [distanceStackRows]int
	rows := stack[:]
	return boundedOSA(a, b, maxEdits, &rows)
}

// boundedOSA is Distance working in memory the caller owns, so that a scan of
// many names (SuggestNames) reuses one buffer. *rows is replaced by a larger
// slice when it is too small; its contents need no initialization and mean
// nothing afterwards.
func boundedOSA(a, b string, maxEdits int, rows *[]int) int {
	if maxEdits < 0 {
		maxEdits = 0
	}

	// A shared prefix or suffix never costs an edit, and an optimal alignment
	// can always keep it aligned, so the table only has to cover the middle.
	// For a near miss - the case that reaches the end of the table - the
	// middle is a few characters long.
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}

	// The distance is symmetric; making a the shorter string gives the table
	// the fewest rows.
	if len(a) > len(b) {
		a, b = b, a
	}
	n, m := len(a), len(b)

	// The distance never exceeds the longer length, so a larger bound cannot
	// change the result. Lowering it keeps the band as narrow as possible and
	// keeps maxEdits+1 from overflowing for a caller that passes math.MaxInt.
	if maxEdits > m {
		maxEdits = m
	}
	// Each character of the length difference needs an edit of its own.
	if m-n > maxEdits {
		return maxEdits + 1
	}
	if n == 0 {
		return m
	}

	// over stands for every value above the bound. Capping at it keeps the
	// arithmetic small and makes the cells outside the band, which are never
	// computed, representable: a cell (i, j) costs at least |i-j|, so outside
	// the band it is over by definition.
	over := maxEdits + 1

	// Three rows of the table, each m+1 wide: the row being filled, the one
	// above it, and the one above that, which a swap reaches back to.
	width := m + 1
	if cap(*rows) < 3*width {
		*rows = make([]int, 3*width)
	}
	buf := (*rows)[:3*width]
	prev2, prev, cur := buf[:width], buf[width:2*width], buf[2*width:]

	// Row 0: turning the empty prefix of a into b[:j] takes j insertions.
	hi := min(m, maxEdits)
	for j := 0; j <= hi; j++ {
		prev[j] = j
	}
	if hi < m {
		prev[hi+1] = over // the cell right of the band, read by the row below
	}

	for i := 1; i <= n; i++ {
		lo := max(1, i-maxEdits)
		hi := min(m, i+maxEdits)

		// The cell left of the band: column 0 (i deletions) while the band
		// still touches it, otherwise a cell outside the band.
		rowMin := over
		if lo == 1 {
			cur[0] = min(i, over)
			rowMin = cur[0]
		} else {
			cur[lo-1] = over
		}

		ca := a[i-1]
		for j := lo; j <= hi; j++ {
			cb := b[j-1]

			v := prev[j-1] // keep the character, or substitute it
			if ca != cb {
				v++
			}
			if d := prev[j] + 1; d < v { // delete a[i-1]
				v = d
			}
			if d := cur[j-1] + 1; d < v { // insert b[j-1]
				v = d
			}
			if i > 1 && j > 1 && ca == b[j-2] && a[i-2] == cb {
				if d := prev2[j-2] + 1; d < v { // swap two adjacent characters
					v = d
				}
			}
			if v > over {
				v = over
			}
			cur[j] = v
			if v < rowMin {
				rowMin = v
			}
		}
		if hi < m {
			cur[hi+1] = over
		}

		// No cell of a row is cheaper than the cheapest cell of the row above
		// (a swap reaches two rows up, but costs at least what the diagonal
		// step in between does), so once a whole row is over the bound the
		// result is too.
		if rowMin > maxEdits {
			return over
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[m]
}
