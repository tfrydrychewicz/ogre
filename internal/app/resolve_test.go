package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
	"github.com/tfrydrychewicz/ogre/internal/store"
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
		want outcome
	}{
		// Hits, ignoring the case of the name.
		{"exact", "/on-call", outcome{DecisionHit, "on-call", onCall}},
		{"capitalised", "/On-Call", outcome{DecisionHit, "on-call", onCall}},
		{"upper case", "/ON-CALL", outcome{DecisionHit, "on-call", onCall}},
		{"no leading slash", "on-call", outcome{DecisionHit, "on-call", onCall}},
		{"trailing slash", "/on-call/", outcome{DecisionHit, "on-call", onCall}},
		{"double slash", "//on-call", outcome{DecisionHit, "on-call", onCall}},
		{"query is ignored", "/on-call?utm=x", outcome{DecisionHit, "on-call", onCall}},
		{"fragment is ignored", "/on-call#now", outcome{DecisionHit, "on-call", onCall}},
		{"percent-encoded letters", "/on%2Dcall", outcome{DecisionHit, "on-call", onCall}},

		// Q5: "-" and "_" are significant. These are other names, not on-call.
		{"separator removed", "/oncall", outcome{DecisionMissUnknown, "oncall", ""}},
		{"underscore for hyphen", "/on_call", outcome{DecisionMissUnknown, "on_call", ""}},
		{"dot for hyphen", "/on.call", outcome{DecisionMissUnknown, "on.call", ""}},
		{"separator removed, upper case", "/OnCall", outcome{DecisionMissUnknown, "oncall", ""}},

		// Several literal routes on one link.
		{"link root", "/gh", outcome{DecisionHit, "gh", "https://github.com/acme"}},
		{"second route", "/gh/new", outcome{DecisionHit, "gh", "https://github.com/organizations/acme/repositories/new"}},
		{"second route, mixed case", "/GH/New", outcome{DecisionHit, "gh", "https://github.com/organizations/acme/repositories/new"}},
		{"three segments", "/gh/settings/billing", outcome{DecisionHit, "gh", "https://github.com/organizations/acme/settings/billing"}},
		{"empty segments inside", "/gh//settings///billing/", outcome{DecisionHit, "gh", "https://github.com/organizations/acme/settings/billing"}},

		// Known name, but no route fits.
		{"unknown second segment", "/gh/old", outcome{DecisionMissKnown, "gh", ""}},
		{"too many segments", "/gh/new/extra", outcome{DecisionMissKnown, "gh", ""}},
		{"too few segments for any route", "/gh/settings", outcome{DecisionMissKnown, "gh", ""}},
		{"extra segment on a one-route link", "/on-call/today", outcome{DecisionMissKnown, "on-call", ""}},
		{"link without a root route", "/docs", outcome{DecisionMissKnown, "docs", ""}},
		{"link without a root route, mixed case", "/Docs/Other", outcome{DecisionMissKnown, "docs", ""}},
		{"encoded slash is one segment", "/gh%2Fnew", outcome{DecisionMissUnknown, "", ""}},
		{"encoded slash in a later segment", "/gh/settings%2Fbilling", outcome{DecisionMissKnown, "gh", ""}},
		{"dot segment is not cleaned away", "/gh/./new", outcome{DecisionMissKnown, "gh", ""}},
		{"dot dot is not followed", "/on-call/../gh", outcome{DecisionMissKnown, "on-call", ""}},
		{"link with only a deeper route hits it", "/docs/api", outcome{DecisionHit, "docs", "https://docs.example.com/api"}},

		// Unknown names.
		{"unknown", "/wiki", outcome{DecisionMissUnknown, "wiki", ""}},
		{"unknown with more segments", "/Wiki/Home/Page", outcome{DecisionMissUnknown, "wiki", ""}},
		{"prefix of a name", "/on-cal", outcome{DecisionMissUnknown, "on-cal", ""}},
		// Unknown first segments that could not be a name at all: a miss
		// like any other, but nothing of the request is repeated.
		{"name with a space", "/on-call%20", outcome{DecisionMissUnknown, "", ""}},
		{"dot dot first", "/../gh", outcome{DecisionMissUnknown, "", ""}},
		{"encoded slash hides more path", "/Secret-Name%2Fsecret-value?token=abc", outcome{DecisionMissUnknown, "", ""}},
		{"line break", "/x%0D%0ASet-Cookie:%20a=b", outcome{DecisionMissUnknown, "", ""}},
		{"NUL", "/a%00b", outcome{DecisionMissUnknown, "", ""}},
		{"markup", "/%3Cscript%3Ealert(1)%3C%2Fscript%3E", outcome{DecisionMissUnknown, "", ""}},
		{"non-ASCII", "/caf%C3%A9", outcome{DecisionMissUnknown, "", ""}},
		{"leading hyphen", "/-x", outcome{DecisionMissUnknown, "", ""}},
		{"longest possible name", "/" + strings.Repeat("A", 200), outcome{DecisionMissUnknown, strings.Repeat("a", 200), ""}},
		{"one character too long", "/" + strings.Repeat("a", 201), outcome{DecisionMissUnknown, "", ""}},
		{"very long", "/" + strings.Repeat("a", 100000), outcome{DecisionMissUnknown, "", ""}},
		{"kelvin sign is not k", "/%E2%84%AA", outcome{DecisionMissUnknown, "", ""}},

		// No go link in the path at all.
		{"empty", "", outcome{DecisionHome, "", ""}},
		{"root", "/", outcome{DecisionHome, "", ""}},
		{"only slashes", "///", outcome{DecisionHome, "", ""}},
		{"only a query", "/?q=x", outcome{DecisionHome, "", ""}},

		// Undecodable paths.
		{"non-hex escape", "%zz", outcome{DecisionBadRequest, "", ""}},
		{"non-hex escape after the name", "/gh/%zz", outcome{DecisionBadRequest, "", ""}},
		{"lone percent", "/%", outcome{DecisionBadRequest, "", ""}},
		{"invalid UTF-8", "/%ff", outcome{DecisionBadRequest, "", ""}},
		{"invalid UTF-8 after a known name", "/on-call/%ff", outcome{DecisionBadRequest, "", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := brief(a.Resolve(tt.path)); got != tt.want {
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

// A hit says which link and route answered and with which values; a
// known-name miss says why each route did not (PRD 5.6). All of it points
// into the snapshot: nothing is copied or compiled for a request.
func TestResolveDecisionDetails(t *testing.T) {
	a, st := newApp(t)
	created := mustCreate(t, a, ghDraft())
	st.calls.Store(0)

	d := a.Resolve("/GH/Ogre%20Server")
	if d.Kind != DecisionHit || d.Destination != "https://github.com/acme/Ogre%20Server" || d.LinkName != "gh" {
		t.Fatalf("Resolve = %+v", brief(d))
	}
	if d.Link == nil || d.Link.ID != created.ID || d.Link.Description != "GitHub shortcuts" || len(d.Link.Routes) != 3 {
		t.Errorf("Link = %+v, want the stored link", d.Link)
	}
	if d.Route == nil || d.Route.ID != created.Routes[2].ID || d.Route.Pattern != "gh/{repo}" {
		t.Errorf("Route = %+v, want gh/{repo}", d.Route)
	}
	// The value is what was typed, decoded, in its own case.
	if want := map[string]string{"repo": "Ogre Server"}; !reflect.DeepEqual(d.Values, want) {
		t.Errorf("Values = %v, want %v", d.Values, want)
	}
	if d.Reasons != nil || d.Suggestions != nil || d.RequestedName != "" || d.Err != nil {
		t.Errorf("a hit carries parts of a miss: %+v", d)
	}
	// The route's variable descriptions come along, for whoever shows them.
	if want := []linkmodel.VariableMeta{{Name: "repo", Description: "Repository", Example: "ogre"}}; !reflect.DeepEqual(d.Route.Variables, want) {
		t.Errorf("Route.Variables = %+v, want %+v", d.Route.Variables, want)
	}

	again := a.Resolve("/gh/other")
	if again.Link != d.Link || again.Route != d.Route {
		t.Error("two hits on one route point at different copies of the link or route")
	}
	if literal := a.Resolve("/gh/new"); literal.Route != &d.Link.Routes[1] || literal.Values != nil {
		t.Errorf("a hit on a literal route = route %+v, values %v", literal.Route, literal.Values)
	}

	miss := a.Resolve("/gh/a/b")
	if miss.Kind != DecisionMissKnown || miss.Link != d.Link || miss.LinkName != "gh" {
		t.Fatalf("Resolve(/gh/a/b) = %+v", brief(miss))
	}
	if miss.Destination != "" || miss.Route != nil || miss.Values != nil || miss.Suggestions != nil || miss.RequestedName != "" {
		t.Errorf("a known-name miss carries parts of another decision: %+v", miss)
	}
	if len(miss.Reasons) != 3 {
		t.Fatalf("Reasons = %+v, want one per route", miss.Reasons)
	}
	for i, r := range miss.Reasons {
		if r.RouteID != created.Routes[i].ID || r.Pattern != created.Routes[i].Pattern || r.Code != linkmodel.ReasonSegmentCount || r.Message == "" {
			t.Errorf("reason %d = %+v", i, r)
		}
	}
	if got := st.calls.Load(); got != 0 {
		t.Errorf("Resolve made %d store calls, want 0", got)
	}
}

// When the link model reports that a destination did not expand safely, the
// decision is an internal error: no destination, nothing to redirect to, and
// an error to log that carries no value of the request.
func TestExpandFailureIsAnInternalError(t *testing.T) {
	a := resolver(Options{}, memLink(t, 1, "", "x/{v}", "https://example.com/{v}"))
	link, _ := a.index.Load().Find("x")
	m, ok, reasons, _ := link.Match([]string{"x", "secret-value"})
	if !ok {
		t.Fatal("the link does not match its own request")
	}
	// What Match returns when the assertion fails, as its test pins it.
	failure := fmt.Errorf("link %q, route %d: %w", "x", 100, linkmodel.ErrExpand)

	for name, d := range map[string]Decision{
		"as Match reports it":            matched(link, linkmodel.Match{}, false, nil, failure),
		"even with a half-made answer":   matched(link, m, true, reasons, failure),
		"even alongside reasons to miss": matched(link, linkmodel.Match{}, false, []linkmodel.Reason{{Code: "x"}}, failure),
	} {
		if d.Kind != DecisionInternalError || d.Kind.String() != "internal_error" {
			t.Errorf("%s: kind = %v, want an internal error", name, d.Kind)
		}
		if d.Destination != "" || d.Route != nil || d.Values != nil || d.Reasons != nil || d.Suggestions != nil {
			t.Errorf("%s: the decision carries something to act on: %+v", name, d)
		}
		if d.LinkName != "x" || d.Link != &link.Link || !errors.Is(d.Err, linkmodel.ErrExpand) {
			t.Errorf("%s: LinkName %q, Link %p, Err %v; want the link and the cause", name, d.LinkName, d.Link, d.Err)
		}
		if strings.Contains(d.Err.Error(), "secret-value") {
			t.Errorf("%s: the error carries a value of the request: %v", name, d.Err)
		}
	}
	// The same seam, when nothing is wrong, is what Resolve returns.
	if got, want := matched(link, m, ok, reasons, nil), a.Resolve("/x/secret-value"); !reflect.DeepEqual(got, want) {
		t.Errorf("matched = %+v, Resolve = %+v", got, want)
	}
}

// New loads every link with its variables and compiles it once. After that a
// request neither reads the store nor compiles a regular expression: a
// compile costs dozens of allocations, a match on a compiled one a handful.
func TestNewLoadsVariablesAndCompilesOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ogre.db")
	first, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.CreateLink(ctx, localActor, Draft{Description: "Daily reports", Routes: []DraftRoute{{
		Pattern:     "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
		Destination: "https://reports.example.com/daily?month={month}&day={day}",
		Variables: []linkmodel.VariableMeta{
			{Name: "day", Description: "Day of the month", Example: "13"},
			{Name: "month", Description: "Month, 1 to 12", Example: "10"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart.
	st := openSpyStore(t, path)
	a, err = New(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.calls.Load(); got != 1 {
		t.Errorf("New made %d store calls, want 1 (one read of everything)", got)
	}
	st.calls.Store(0)

	d := a.Resolve("/reports/10/13")
	if d.Kind != DecisionHit || d.Destination != "https://reports.example.com/daily?month=10&day=13" {
		t.Fatalf("after a restart, Resolve = %+v", brief(d))
	}
	// In pattern order, whatever order they were sent in.
	want := []linkmodel.VariableMeta{
		{Name: "month", Description: "Month, 1 to 12", Example: "10"},
		{Name: "day", Description: "Day of the month", Example: "13"},
	}
	if !reflect.DeepEqual(d.Route.Variables, want) {
		t.Errorf("Route.Variables after a restart = %+v, want %+v", d.Route.Variables, want)
	}
	if infos := linkmodel.RouteVariables(*d.Route); len(infos) != 2 || infos[0].Type != linkmodel.TypeRegex || infos[1].Type != linkmodel.TypeInt {
		t.Errorf("RouteVariables = %+v", infos)
	}
	misses(t, a, "/reports/13/13", DecisionMissKnown)

	allocs := testing.AllocsPerRun(200, func() {
		if d := a.Resolve("/reports/10/13"); d.Kind != DecisionHit {
			t.Fatalf("got %v, want a hit", d.Kind)
		}
	})
	// The segments, the values map and the destination text. The exact
	// number is the link model's business; the bound is far below what one
	// regexp.Compile costs.
	if allocs > 8 {
		t.Errorf("a hit on a regex route allocated %v times, want at most 8: is something compiled per request?", allocs)
	}
	if got := st.calls.Load(); got != 0 {
		t.Errorf("resolving made %d store calls, want 0", got)
	}
}

// Readers resolve and validate while links are updated, renamed, deleted and
// re-created. Under -race this fails on any unsynchronised access between
// the write paths and the read path. Each reader also checks that every
// answer is one of the states a writer committed, whole: never a mix of two
// versions, never a destination of a link that is gone.
func TestConcurrentResolveDuringWrites(t *testing.T) {
	const (
		readers = 8
		rounds  = 40
	)
	ctx := context.Background()
	a, _ := newApp(t)
	flip := mustCreate(t, a, draft("flip/{n:int}", "https://a.example/{n}"))
	mover := mustCreate(t, a, draft("mover-a/{x}", "https://m.example/{x}"))
	mustCreate(t, a, draft("steady", "https://example.com/steady"))

	var (
		done = make(chan struct{})
		wg   sync.WaitGroup
	)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if d := a.Resolve("/steady"); d.Kind != DecisionHit || d.Destination != "https://example.com/steady" {
					t.Errorf("Resolve(/steady) = %+v during writes", brief(d))
					return
				}
				// The link that flips between two destinations: always a
				// hit, on one of the two, and the template it came from is
				// the one in the link the decision points at.
				d := a.Resolve("/flip/7")
				if d.Kind != DecisionHit || (d.Destination != "https://a.example/7" && d.Destination != "https://b.example/7") {
					t.Errorf("Resolve(/flip/7) = %+v during writes", brief(d))
					return
				}
				if template := d.Link.Routes[0].Destination; strings.Replace(template, "{n}", "7", 1) != d.Destination || d.Route != &d.Link.Routes[0] {
					t.Errorf("hit on %q from a link whose template is %q", d.Destination, template)
					return
				}
				// The link that is renamed back and forth: under either
				// name a hit on its own destination, or an unknown name
				// whose suggestion, if there is one, is the other name with
				// the value carried over.
				for _, name := range []string{"mover-a", "mover-b"} {
					d := a.Resolve("/" + name + "/v")
					switch d.Kind {
					case DecisionHit:
						if d.Destination != "https://m.example/v" || d.Link.Name != name {
							t.Errorf("Resolve(/%s/v) = %+v", name, brief(d))
							return
						}
					case DecisionMissUnknown:
						for _, s := range d.Suggestions {
							if !strings.HasPrefix(s.Name, "mover-") || s.Name == name || !s.Resolves || s.Destination != "https://m.example/v" {
								t.Errorf("Resolve(/%s/v) suggests %+v", name, s)
								return
							}
						}
					default:
						t.Errorf("Resolve(/%s/v) = %+v during writes", name, brief(d))
						return
					}
				}
				// The link that comes and goes.
				if d := a.Resolve("/gone"); d.Kind != DecisionMissUnknown && (d.Kind != DecisionHit || d.Destination != "https://example.com/gone") {
					t.Errorf("Resolve(/gone) = %+v during writes", brief(d))
					return
				}
				// Validate reads the same snapshot.
				if res := a.Validate(ctx, localActor, ValidateRequest{Draft: draft("steady", "https://x.example")}); res.Name.Available {
					t.Error("Validate says the name of a link that always exists is free")
					return
				}
			}
		}()
	}

	fail := func(what string, err error) bool {
		if err != nil {
			t.Errorf("%s: %v", what, err)
		}
		return err != nil
	}
	for i := 0; i < rounds; i++ {
		host := []string{"b", "a"}[i%2]
		next, err := a.UpdateLink(ctx, localActor, flip.ID, Draft{Routes: []DraftRoute{
			{ID: flip.Routes[0].ID, Pattern: "flip/{n:int}", Destination: "https://" + host + ".example/{n}"}}}, flip.Version)
		if fail("update", err) {
			break
		}
		flip = next

		name := []string{"mover-b", "mover-a"}[i%2]
		next, err = a.UpdateLink(ctx, localActor, mover.ID, Draft{Routes: []DraftRoute{
			{ID: mover.Routes[0].ID, Pattern: name + "/{x}", Destination: "https://m.example/{x}"}}}, mover.Version)
		if fail("rename", err) {
			break
		}
		mover = next

		gone, err := a.CreateLink(ctx, localActor, draft("gone", "https://example.com/gone"))
		if fail("create", err) || fail("delete", a.DeleteLink(ctx, localActor, gone.ID, gone.Version)) {
			break
		}
	}
	close(done)
	wg.Wait()

	// rounds is even: everything is back where it started, many versions on.
	hits(t, a, "/flip/7", "https://a.example/7")
	hits(t, a, "/mover-a/v", "https://m.example/v")
	misses(t, a, "/mover-b/v", DecisionMissUnknown)
	misses(t, a, "/gone", DecisionMissUnknown)
	if flip.Version != rounds+1 || mover.Version != rounds+1 || a.index.Load().Len() != 3 {
		t.Errorf("after %d rounds: versions %d and %d, %d links in the index", rounds, flip.Version, mover.Version, a.index.Load().Len())
	}
}
