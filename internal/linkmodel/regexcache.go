package linkmodel

import (
	"regexp"
	"runtime"
	"sync"
	"weak"
)

// sharedRegexps lets every route that uses the same regular expression share
// one compiled *regexp.Regexp instead of each holding its own.
//
// A compiled expression is large next to the text it comes from: about a
// kilobyte for one as short as [A-Z]+-[0-9]+. The index compiles every route
// of every link at startup, and expressions repeat across a Directory (a
// ticket key, a month, a slug), so at 100,000 links separate copies were a
// quarter of the server's heap and pushed its peak memory past the 512 MB of
// NFR-5 (docs/perf.md). Sharing is safe because a *regexp.Regexp is safe for
// concurrent use and nothing in this package changes one after compiling it.
//
// The map holds weak pointers, so it never keeps an expression alive: once
// the last route that uses it is gone (the link was edited or deleted, or
// the expression was only typed into a form), the collector frees it and a
// cleanup removes its entry. The cache therefore costs nothing for
// expressions nobody uses any more, however many have been typed.
var sharedRegexps = struct {
	mu sync.Mutex
	m  map[string]weak.Pointer[regexp.Regexp]
}{m: map[string]weak.Pointer[regexp.Regexp]{}}

// compileShared returns the compiled form of expr, the one already in use
// when there is one. It fails as regexp.Compile does.
func compileShared(expr string) (*regexp.Regexp, error) {
	if re := lookupShared(expr); re != nil {
		return re, nil
	}
	// Compile without the lock: compiling takes far longer than a lookup,
	// and two callers compiling the same expression at once is harmless.
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	sharedRegexps.mu.Lock()
	defer sharedRegexps.mu.Unlock()
	if old := sharedRegexps.m[expr].Value(); old != nil {
		return old, nil // another caller got there first
	}
	sharedRegexps.m[expr] = weak.Make(re)
	runtime.AddCleanup(re, forgetShared, expr)
	return re, nil
}

func lookupShared(expr string) *regexp.Regexp {
	sharedRegexps.mu.Lock()
	defer sharedRegexps.mu.Unlock()
	return sharedRegexps.m[expr].Value()
}

// forgetShared removes the entry of expr once its expression has been
// collected. A later compileShared may already have stored a new, live one
// under the same key; that one stays.
func forgetShared(expr string) {
	sharedRegexps.mu.Lock()
	defer sharedRegexps.mu.Unlock()
	if wp, ok := sharedRegexps.m[expr]; ok && wp.Value() == nil {
		delete(sharedRegexps.m, expr)
	}
}
