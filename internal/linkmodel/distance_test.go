package linkmodel

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

// referenceOSA is the textbook optimal-string-alignment distance: the whole
// table, no bound, no shortcut. It is deliberately the plainest possible
// implementation, so that Distance can be checked against it.
func referenceOSA(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := d[i-1][j-1] + cost
			v = min(v, d[i-1][j]+1, d[i][j-1]+1)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, d[i-2][j-2]+1)
			}
			d[i][j] = v
		}
	}
	return d[len(a)][len(b)]
}

// wantBounded is what Distance must return for a pair whose true distance is
// known: the contract of the early exit.
func wantBounded(distance, maxEdits int) int {
	if maxEdits < 0 {
		maxEdits = 0
	}
	if maxEdits == math.MaxInt {
		return distance
	}
	return min(distance, maxEdits+1)
}

func TestDistance(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		// Classic pairs.
		{"kitten sitting", "kitten", "sitting", 3},
		{"flaw lawn", "flaw", "lawn", 2},
		{"saturday sunday", "saturday", "sunday", 3},
		{"one substitution", "a", "b", 1},
		{"nothing in common", "abc", "xyz", 3},

		// What makes it optimal string alignment and not unrestricted
		// Damerau-Levenshtein, which would answer 2: ca -> ac -> abc edits
		// the swapped pair a second time.
		{"osa not full damerau", "ca", "abc", 3},
		{"rotation is not two swaps", "abc", "cab", 2},

		// Adjacent swaps.
		{"prd example 6", "reprots", "reports", 1},
		{"swap of a whole string", "ab", "ba", 1},
		{"two separate swaps", "abcd", "badc", 2},
		{"swap at the start", "jria", "jira", 1},
		{"swap at the end", "jiar", "jira", 1},
		{"swap of equal letters is nothing", "aab", "aab", 0},
		{"swap next to a repeated letter", "aab", "aba", 1},
		{"letters two apart are not a swap", "abc", "cba", 2},

		// PRD example 15a: a separator is an edit like any other.
		{"missing separator", "oncall", "on-call", 1},
		{"different separator", "on_call", "on-call", 1},
		{"extra separator", "on_call", "oncall", 1},
		{"dot separator", "v1.2", "v1-2", 1},

		// Empty and equal strings.
		{"both empty", "", "", 0},
		{"empty to text", "", "abc", 3},
		{"text to empty", "abc", "", 3},
		{"equal", "reports", "reports", 0},
		{"equal single", "a", "a", 0},

		// Insertions and deletions at either end and in the middle.
		{"prefix", "report", "reports", 1},
		{"suffix", "eports", "reports", 1},
		{"middle", "repots", "reports", 1},
		{"doubled letter", "repports", "reports", 1},
		{"two insertions", "rpots", "reports", 2},

		// Case is not folded here; callers pass lowercase names.
		{"case counts", "Reports", "reports", 1},

		// Not a name at all: bytes are compared, and nothing panics.
		{"non-ascii is compared as bytes", "é", "e", 2},
		{"invalid utf-8", "\xff\xfe", "\xfe\xff", 1},
		{"nul bytes", "a\x00b", "ab", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ref := referenceOSA(tt.a, tt.b); ref != tt.want {
				t.Fatalf("referenceOSA(%q, %q) = %d, want %d: the table or the reference is wrong", tt.a, tt.b, ref, tt.want)
			}
			// Unbounded, then with every bound around the true distance: the
			// result is the distance while it fits and bound+1 once it does not.
			for _, maxEdits := range []int{math.MaxInt, 1000, tt.want + 2, tt.want + 1, tt.want, tt.want - 1, tt.want - 2, 1, 0, -1} {
				want := wantBounded(tt.want, maxEdits)
				if got := Distance(tt.a, tt.b, maxEdits); got != want {
					t.Errorf("Distance(%q, %q, %d) = %d, want %d", tt.a, tt.b, maxEdits, got, want)
				}
				if got := Distance(tt.b, tt.a, maxEdits); got != want {
					t.Errorf("Distance(%q, %q, %d) = %d, want %d (not symmetric)", tt.b, tt.a, maxEdits, got, want)
				}
			}
		})
	}
}

// TestDistanceBoundSemantics pins what the third argument means at its edges,
// because callers compare the result with the bound.
func TestDistanceBoundSemantics(t *testing.T) {
	tests := []struct {
		name     string
		a, b     string
		maxEdits int
		want     int
	}{
		{"within the bound", "reprots", "reports", 2, 1},
		{"exactly the bound", "kitten", "sitting", 3, 3},
		{"one over the bound", "kitten", "sitting", 2, 3},
		{"far over the bound", "kitten", "sitting", 0, 1},
		{"zero bound, equal", "gh", "gh", 0, 0},
		{"zero bound, different", "gh", "gj", 0, 1},
		{"length difference alone exceeds the bound", "a", "abcdefgh", 2, 3},
		{"negative bound means zero, equal", "gh", "gh", -7, 0},
		{"negative bound means zero, different", "gh", "jira", -7, 1},
		{"largest bound does not overflow", "kitten", "sitting", math.MaxInt, 3},
		{"largest bound, empty", "", "", math.MaxInt, 0},
		{"smallest bound", "kitten", "sitting", math.MinInt, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Distance(tt.a, tt.b, tt.maxEdits); got != tt.want {
				t.Errorf("Distance(%q, %q, %d) = %d, want %d", tt.a, tt.b, tt.maxEdits, got, tt.want)
			}
		})
	}
}

// randomPair returns two strings that are sometimes unrelated and sometimes a
// few edits apart, over an alphabet small enough that swaps, repeats and
// accidental matches happen all the time. Random unrelated strings alone would
// almost never reach the interesting part of the table.
func randomPair(r *rand.Rand, alphabet string, maxLen int) (a, b string) {
	randomString := func(n int) []byte {
		s := make([]byte, n)
		for i := range s {
			s[i] = alphabet[r.Intn(len(alphabet))]
		}
		return s
	}
	x := randomString(r.Intn(maxLen + 1))
	if r.Intn(4) == 0 {
		return string(x), string(randomString(r.Intn(maxLen + 1)))
	}
	y := append([]byte(nil), x...)
	for edits := r.Intn(5); edits > 0; edits-- {
		switch op := r.Intn(4); {
		case op == 0 && len(y) < maxLen: // insert
			at := r.Intn(len(y) + 1)
			y = append(y[:at], append([]byte{alphabet[r.Intn(len(alphabet))]}, y[at:]...)...)
		case op == 1 && len(y) > 0: // delete
			at := r.Intn(len(y))
			y = append(y[:at], y[at+1:]...)
		case op == 2 && len(y) > 0: // substitute
			y[r.Intn(len(y))] = alphabet[r.Intn(len(alphabet))]
		case op == 3 && len(y) > 1: // swap two adjacent characters
			at := r.Intn(len(y) - 1)
			y[at], y[at+1] = y[at+1], y[at]
		}
	}
	return string(x), string(y)
}

// TestDistanceMatchesReference is the correctness of the early exit: for
// random pairs and every bound, Distance equals min(true distance, bound+1),
// in both directions.
func TestDistanceMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(20261013))
	groups := []struct {
		name     string
		alphabet string
		maxLen   int // 100 goes past the stack buffer, so the heap path is covered too
		pairs    int
	}{
		{"two letters, short", "ab", 8, 8000},
		{"name characters, short", "abc-_", 12, 8000},
		{"name characters, typical", "abcdefghijklmnopqrstuvwxyz0123456789-_.", 24, 4000},
		{"two letters, long", "ab", 100, 500},
		{"name characters, long", "abc-_", 100, 500},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			for n := 0; n < g.pairs; n++ {
				a, b := randomPair(r, g.alphabet, g.maxLen)
				ref := referenceOSA(a, b)
				for _, maxEdits := range []int{0, 1, 2, 3, ref - 1, ref, ref + 1, r.Intn(g.maxLen + 2), math.MaxInt} {
					want := wantBounded(ref, maxEdits)
					if got := Distance(a, b, maxEdits); got != want {
						t.Fatalf("Distance(%q, %q, %d) = %d, want %d (true distance %d)", a, b, maxEdits, got, want, ref)
					}
					if got := Distance(b, a, maxEdits); got != want {
						t.Fatalf("Distance(%q, %q, %d) = %d, want %d (not symmetric; true distance %d)", b, a, maxEdits, got, want, ref)
					}
				}
			}
		})
	}
}

// TestDistanceReusedBuffer checks that nothing left in the buffer by one pair
// leaks into the next: SuggestNames runs a whole scan in one buffer, and the
// table deliberately does not clear it.
func TestDistanceReusedBuffer(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	var rows []int
	for n := 0; n < 20000; n++ {
		a, b := randomPair(r, "abc-_", 20)
		maxEdits := r.Intn(5)
		want := wantBounded(referenceOSA(a, b), maxEdits)
		if got := boundedOSA(a, b, maxEdits, &rows); got != want {
			t.Fatalf("pair %d: boundedOSA(%q, %q, %d) = %d with a reused buffer, want %d", n, a, b, maxEdits, got, want)
		}
	}
}

// TestDistanceDoesNotAllocate keeps the scan of every name free of garbage:
// names of realistic length are compared on the stack.
func TestDistanceDoesNotAllocate(t *testing.T) {
	a, b := strings.Repeat("ab-", 21), strings.Repeat("cd_", 21) // 63 bytes each, nothing shared
	var sink int
	allocs := testing.AllocsPerRun(100, func() {
		sink += Distance(a, b, 63)
		sink += Distance("reprots", "reports", 2)
	})
	if allocs != 0 {
		t.Errorf("Distance allocated %v times per run, want 0", allocs)
	}
	if sink == 0 {
		t.Error("Distance returned 0 for different strings")
	}
}

// FuzzDistance checks Distance against the reference for arbitrary bytes and
// bounds: the same result, the same in both directions, and no panic.
func FuzzDistance(f *testing.F) {
	seeds := []struct {
		a, b     string
		maxEdits int
	}{
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 1},
		{"ca", "abc", 2},
		{"reprots", "reports", 1},
		{"oncall", "on-call", 1},
		{"on_call", "on-call", 0},
		{"", "", 0},
		{"", "abc", 2},
		{"abcd", "badc", 5},
		{"é", "e", -1},
		{"\xff\xfe", "\xfe\xff", math.MaxInt},
		{strings.Repeat("ab", 40), strings.Repeat("ba", 40), 80},
	}
	for _, s := range seeds {
		f.Add(s.a, s.b, s.maxEdits)
	}
	f.Fuzz(func(t *testing.T, a, b string, maxEdits int) {
		if len(a) > 400 || len(b) > 400 {
			t.Skip("the reference fills the whole table; longer than any pattern is not worth the time")
		}
		ref := referenceOSA(a, b)
		want := wantBounded(ref, maxEdits)
		if got := Distance(a, b, maxEdits); got != want {
			t.Errorf("Distance(%q, %q, %d) = %d, want %d (true distance %d)", a, b, maxEdits, got, want, ref)
		}
		if got := Distance(b, a, maxEdits); got != want {
			t.Errorf("Distance(%q, %q, %d) = %d, want %d (not symmetric; true distance %d)", b, a, maxEdits, got, want, ref)
		}
		// Bounds drawn from the distance itself, which the fuzzer would
		// rarely hit by chance, and they are where an early exit goes wrong.
		for _, m := range []int{ref - 1, ref, ref + 1} {
			if got, want := Distance(a, b, m), wantBounded(ref, m); got != want {
				t.Errorf("Distance(%q, %q, %d) = %d, want %d (true distance %d)", a, b, m, got, want, ref)
			}
		}
	})
}

func BenchmarkDistance(b *testing.B) {
	benchmarks := []struct {
		name     string
		a, b     string
		maxEdits int
	}{
		{"near miss", "quarterly-reprots", "quarterly-reports", 2},
		{"unrelated", "quarterly-reports", "on-call-schedule", 2},
		{"shared prefix", "team-platform-roadmap", "team-payments-runbook", 2},
		{"unbounded", "quarterly-reports", "on-call-schedule", math.MaxInt},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Distance(bm.a, bm.b, bm.maxEdits)
			}
		})
	}
}
