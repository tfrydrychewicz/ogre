package linkmodel

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// Routes that use the same expression share one compiled program: that is
// what keeps 100,000 links under NFR-5's memory (docs/perf.md). Both a
// pattern read back from the store and one being saved get it, and the
// shared program still decides like a private one would.
func TestRegexTypesShareTheirProgram(t *testing.T) {
	a, ok := StoredPattern("a/{key:regex([A-Z]+-[0-9]+)}")
	if !ok {
		t.Fatal("stored pattern a did not parse")
	}
	b := mustParse(t, "b/{id:regex([A-Z]+-[0-9]+)}")
	c := mustParse(t, "c/{key:regex([a-z]+)}")

	reOf := func(p Pattern) regexType { return p.Segments()[1].Type.(regexType) }
	if reOf(a).re != reOf(b).re {
		t.Error("the same expression in two patterns was compiled twice")
	}
	if reOf(a).re == reOf(c).re {
		t.Error("different expressions share a program")
	}
	for _, v := range []string{"OPS-42", "ops-42", "OPS-", "OPS-42x"} {
		want := v == "OPS-42"
		if got := reOf(b).Accepts(v); got != want {
			t.Errorf("Accepts(%q) = %v, want %v", v, got, want)
		}
	}
	runtime.KeepAlive(a)
}

// The cache keeps nothing alive: once no route uses an expression, its
// entry goes away, so typing many expressions into a form cannot grow the
// server's memory.
func TestSharedRegexpsForgetUnusedExpressions(t *testing.T) {
	const expr = `^(?:only-in-this-test-[0-9]+)$`
	re, err := compileShared(expr)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := compileShared(expr); again != re {
		t.Fatal("a live expression was compiled again")
	}
	re = nil // the last reference

	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		sharedRegexps.mu.Lock()
		_, held := sharedRegexps.m[expr]
		sharedRegexps.mu.Unlock()
		if !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the entry of an expression nobody uses is still in the cache")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = re
}

// Compiling the same expression from many goroutines at once is safe (run
// with -race) and still ends with one shared program.
func TestCompileSharedConcurrently(t *testing.T) {
	const expr = `^(?:concurrent-[a-z]+)$`
	const n = 16
	got := make([]any, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			re, err := compileShared(expr)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = re
		})
	}
	wg.Wait()
	final, _ := compileShared(expr)
	// Goroutines that compiled at the same moment may each have returned
	// their own program, which is harmless; but once one is stored, later
	// callers get it.
	if again, _ := compileShared(expr); again != final {
		t.Error("two calls after the race returned different programs")
	}
	runtime.KeepAlive(got)
}
