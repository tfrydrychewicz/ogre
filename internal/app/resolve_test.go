package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/godir/internal/linkmodel"
)

func TestCreateThenResolve(t *testing.T) {
	a, _ := newApp(t)
	mustCreate(t, a, draft("on-call", "https://pager.example.com/schedule"))
	mustCreate(t, a, draft(
		"gh", "https://github.com/acme",
		"gh/new", "https://github.com/organizations/acme/repositories/new",
		"gh/settings/billing", "https://github.com/organizations/acme/settings/billing",
	))
	mustCreate(t, a, draft("docs/api", "https://docs.example.com/api"))

	const onCall = "https://pager.example.com/schedule"
	tests := []struct {
		name string
		path string
		want Decision
	}{
		// Hits, ignoring the case of the name.
		{"exact", "/on-call", Decision{DecisionHit, "on-call", onCall}},
		{"capitalised", "/On-Call", Decision{DecisionHit, "on-call", onCall}},
		{"upper case", "/ON-CALL", Decision{DecisionHit, "on-call", onCall}},
		{"no leading slash", "on-call", Decision{DecisionHit, "on-call", onCall}},
		{"trailing slash", "/on-call/", Decision{DecisionHit, "on-call", onCall}},
		{"double slash", "//on-call", Decision{DecisionHit, "on-call", onCall}},
		{"query is ignored", "/on-call?utm=x", Decision{DecisionHit, "on-call", onCall}},
		{"fragment is ignored", "/on-call#now", Decision{DecisionHit, "on-call", onCall}},
		{"percent-encoded letters", "/on%2Dcall", Decision{DecisionHit, "on-call", onCall}},

		// Q5: "-" and "_" are significant. These are other names, not on-call.
		{"separator removed", "/oncall", Decision{DecisionMissUnknown, "oncall", ""}},
		{"underscore for hyphen", "/on_call", Decision{DecisionMissUnknown, "on_call", ""}},
		{"dot for hyphen", "/on.call", Decision{DecisionMissUnknown, "on.call", ""}},
		{"separator removed, upper case", "/OnCall", Decision{DecisionMissUnknown, "oncall", ""}},

		// Several literal routes on one link.
		{"link root", "/gh", Decision{DecisionHit, "gh", "https://github.com/acme"}},
		{"second route", "/gh/new", Decision{DecisionHit, "gh", "https://github.com/organizations/acme/repositories/new"}},
		{"second route, mixed case", "/GH/New", Decision{DecisionHit, "gh", "https://github.com/organizations/acme/repositories/new"}},
		{"three segments", "/gh/settings/billing", Decision{DecisionHit, "gh", "https://github.com/organizations/acme/settings/billing"}},
		{"empty segments inside", "/gh//settings///billing/", Decision{DecisionHit, "gh", "https://github.com/organizations/acme/settings/billing"}},

		// Known name, but no route fits.
		{"unknown second segment", "/gh/old", Decision{DecisionMissKnown, "gh", ""}},
		{"too many segments", "/gh/new/extra", Decision{DecisionMissKnown, "gh", ""}},
		{"too few segments for any route", "/gh/settings", Decision{DecisionMissKnown, "gh", ""}},
		{"extra segment on a one-route link", "/on-call/today", Decision{DecisionMissKnown, "on-call", ""}},
		{"link without a root route", "/docs", Decision{DecisionMissKnown, "docs", ""}},
		{"link without a root route, mixed case", "/Docs/Other", Decision{DecisionMissKnown, "docs", ""}},
		{"encoded slash is one segment", "/gh%2Fnew", Decision{DecisionMissUnknown, "", ""}},
		{"encoded slash in a later segment", "/gh/settings%2Fbilling", Decision{DecisionMissKnown, "gh", ""}},
		{"dot segment is not cleaned away", "/gh/./new", Decision{DecisionMissKnown, "gh", ""}},
		{"dot dot is not followed", "/on-call/../gh", Decision{DecisionMissKnown, "on-call", ""}},
		{"link with only a deeper route hits it", "/docs/api", Decision{DecisionHit, "docs", "https://docs.example.com/api"}},

		// Unknown names.
		{"unknown", "/wiki", Decision{DecisionMissUnknown, "wiki", ""}},
		{"unknown with more segments", "/Wiki/Home/Page", Decision{DecisionMissUnknown, "wiki", ""}},
		{"prefix of a name", "/on-cal", Decision{DecisionMissUnknown, "on-cal", ""}},
		// Unknown first segments that could not be a name at all: a miss
		// like any other, but nothing of the request is repeated.
		{"name with a space", "/on-call%20", Decision{DecisionMissUnknown, "", ""}},
		{"dot dot first", "/../gh", Decision{DecisionMissUnknown, "", ""}},
		{"encoded slash hides more path", "/Secret-Name%2Fsecret-value?token=abc", Decision{DecisionMissUnknown, "", ""}},
		{"line break", "/x%0D%0ASet-Cookie:%20a=b", Decision{DecisionMissUnknown, "", ""}},
		{"NUL", "/a%00b", Decision{DecisionMissUnknown, "", ""}},
		{"markup", "/%3Cscript%3Ealert(1)%3C%2Fscript%3E", Decision{DecisionMissUnknown, "", ""}},
		{"non-ASCII", "/caf%C3%A9", Decision{DecisionMissUnknown, "", ""}},
		{"leading hyphen", "/-x", Decision{DecisionMissUnknown, "", ""}},
		{"longest possible name", "/" + strings.Repeat("A", 200), Decision{DecisionMissUnknown, strings.Repeat("a", 200), ""}},
		{"one character too long", "/" + strings.Repeat("a", 201), Decision{DecisionMissUnknown, "", ""}},
		{"very long", "/" + strings.Repeat("a", 100000), Decision{DecisionMissUnknown, "", ""}},
		{"kelvin sign is not k", "/%E2%84%AA", Decision{DecisionMissUnknown, "", ""}},

		// No go link in the path at all.
		{"empty", "", Decision{DecisionHome, "", ""}},
		{"root", "/", Decision{DecisionHome, "", ""}},
		{"only slashes", "///", Decision{DecisionHome, "", ""}},
		{"only a query", "/?q=x", Decision{DecisionHome, "", ""}},

		// Undecodable paths.
		{"non-hex escape", "%zz", Decision{DecisionBadRequest, "", ""}},
		{"non-hex escape after the name", "/gh/%zz", Decision{DecisionBadRequest, "", ""}},
		{"lone percent", "/%", Decision{DecisionBadRequest, "", ""}},
		{"invalid UTF-8", "/%ff", Decision{DecisionBadRequest, "", ""}},
		{"invalid UTF-8 after a known name", "/on-call/%ff", Decision{DecisionBadRequest, "", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Resolve(tt.path); got != tt.want {
				t.Errorf("Resolve(%q) = {%v %q %q}, want {%v %q %q}", tt.path,
					got.Kind, got.LinkName, got.Destination, tt.want.Kind, tt.want.LinkName, tt.want.Destination)
			}
		})
	}
}

// NFR-17: LinkName is what request logs record. It must be the lowercase
// first segment only, whatever else the path carries.
func TestDecisionLinkNameIsOnlyTheName(t *testing.T) {
	a, _ := newApp(t)
	mustCreate(t, a, draft("gh", "https://github.com/acme", "gh/new", "https://github.com/new"))

	paths := []string{
		"/gh", "/GH", "/gh/new", "/GH/NEW", "/gh/SECRET-token/more",
		"/Gh/Secret?token=SECRET#SECRET", "/Unknown/SECRET/value", "/UNKNOWN?token=SECRET",
		"/gh/a%2FSECRET", "//Gh//SECRET//",
	}
	for _, path := range paths {
		d := a.Resolve(path)
		segments, err := linkmodel.SplitPath(path)
		if err != nil {
			t.Fatalf("SplitPath(%q): %v", path, err)
		}
		if want := strings.ToLower(segments[0]); d.LinkName != want {
			t.Errorf("Resolve(%q).LinkName = %q, want %q", path, d.LinkName, want)
		}
		if d.LinkName != strings.ToLower(d.LinkName) {
			t.Errorf("Resolve(%q).LinkName = %q is not lowercase", path, d.LinkName)
		}
		if strings.Contains(strings.ToLower(d.LinkName), "secret") || strings.ContainsAny(d.LinkName, "/?#") {
			t.Errorf("Resolve(%q).LinkName = %q carries more than the name", path, d.LinkName)
		}
	}
	for _, path := range []string{"", "/", "/%zz", "/SECRET%zz"} {
		if d := a.Resolve(path); d.LinkName != "" || d.Destination != "" {
			t.Errorf("Resolve(%q) = %+v, want no name and no destination", path, d)
		}
	}
	// "%2F" keeps further parts of the path inside the first segment. Such a
	// segment cannot be a name, so none of it is passed on.
	for _, path := range []string{
		"/unknown%2FSECRET", "/Unknown%2fSECRET%2Fvalue?token=SECRET", "/gh%2FSECRET", "/%2FSECRET",
		"/SECRET%20token", "/SECRET%0Avalue", "/" + strings.Repeat("SECRET", 40),
	} {
		d := a.Resolve(path)
		if d.Kind != DecisionMissUnknown || d.LinkName != "" || d.Destination != "" {
			t.Errorf("Resolve(%q) = %+v, want an unknown-name miss that names nothing", path, d)
		}
	}
	// Whatever the request, a name that is passed on could be a link name.
	for _, path := range append(paths, "/a%2Fb", "/%E2%84%AA", "/x%0D%0Ay", "/..", "/.well-known/x", "/-/x") {
		if d := a.Resolve(path); d.LinkName != "" && !linkmodel.PossibleName(d.LinkName) {
			t.Errorf("Resolve(%q).LinkName = %q, which could not be a link name", path, d.LinkName)
		}
	}
}

// The resolver answers from memory: no request, hit or miss, reaches the
// store (plan 3.2).
func TestResolveNeverTouchesTheStore(t *testing.T) {
	a, st := newApp(t)
	mustCreate(t, a, draft("gh", "https://github.com/acme"))
	st.calls.Store(0)

	for _, path := range []string{"/gh", "/GH", "/gh/x", "/nope", "", "/", "/%zz", "/%ff"} {
		a.Resolve(path)
	}
	if got := st.calls.Load(); got != 0 {
		t.Errorf("Resolve made %d store calls, want 0", got)
	}
}

// A hit must not allocate beyond splitting the path: the redirect is the hot
// path. This pins the cost so a later change that copies routes or patterns
// per request shows up.
func TestResolveHitAllocations(t *testing.T) {
	a, _ := newApp(t)
	mustCreate(t, a, draft("gh", "https://github.com/acme", "gh/settings/billing", "https://github.com/billing"))

	allocs := testing.AllocsPerRun(200, func() {
		if d := a.Resolve("/gh/settings/billing"); d.Kind != DecisionHit {
			t.Fatalf("got %v, want a hit", d.Kind)
		}
	})
	// One allocation: the slice of segments. The segments themselves are
	// substrings of the path and nothing is copied out of the snapshot.
	if allocs > 1 {
		t.Errorf("Resolve allocated %v times per hit, want at most 1", allocs)
	}
}

// Readers resolve while links are being created. Under -race this fails on
// any unsynchronised access between the write path and the read path. Each
// reader also checks the guarantee the swap gives: a link that CreateLink has
// returned is resolvable from then on, and resolves to its own destination.
func TestConcurrentResolveDuringCreate(t *testing.T) {
	const (
		readers = 8
		links   = 200
	)
	a, _ := newApp(t)
	mustCreate(t, a, draft("seed", "https://example.com/seed"))

	var (
		created sync.Map // name -> destination, stored after CreateLink returned
		done    = make(chan struct{})
		wg      sync.WaitGroup
	)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
				}
				if d := a.Resolve("/SEED"); d.Kind != DecisionHit || d.Destination != "https://example.com/seed" {
					t.Errorf("Resolve(/SEED) = %+v during writes", d)
					return
				}
				name := fmt.Sprintf("link-%d", (i*7+r)%links)
				// Read "was it created" first: only a link known to exist
				// before the request is guaranteed to resolve.
				want, wasCreated := created.Load(name)
				d := a.Resolve("/" + name + "/sub")
				switch {
				case d.Kind == DecisionHit:
					if d.LinkName != name || d.Destination != "https://example.com/"+name+"/sub" {
						t.Errorf("Resolve(%s/sub) = %+v", name, d)
						return
					}
				case wasCreated:
					t.Errorf("Resolve(%s/sub) = %+v after CreateLink returned %v", name, d, want)
					return
				case d.Kind != DecisionMissUnknown:
					t.Errorf("Resolve(%s/sub) = %+v before the link exists", name, d)
					return
				}
			}
		}(r)
	}

	// Two writers, so the write path is exercised concurrently as well.
	var writers sync.WaitGroup
	for w := 0; w < 2; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := w; i < links; i += 2 {
				name := fmt.Sprintf("link-%d", i)
				_, err := a.CreateLink(context.Background(), localActor, draft(
					name, "https://example.com/"+name,
					name+"/sub", "https://example.com/"+name+"/sub",
				))
				if err != nil {
					t.Errorf("CreateLink(%s): %v", name, err)
					return
				}
				created.Store(name, true)
			}
		}(w)
	}
	writers.Wait()
	close(done)
	wg.Wait()

	// Nothing was lost between the two writers' snapshot swaps.
	for i := 0; i < links; i++ {
		name := fmt.Sprintf("link-%d", i)
		if d := a.Resolve("/" + name); d.Kind != DecisionHit {
			t.Errorf("after all writes, Resolve(/%s) = %+v, want a hit", name, d)
		}
	}
}
