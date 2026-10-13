package linkmodel

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// link builds a link from pattern, destination pairs. Route IDs count from 1
// and positions from 0, in the order given.
func link(name string, pairs ...string) Link {
	l := Link{Name: name}
	for i := 0; i+1 < len(pairs); i += 2 {
		l.Routes = append(l.Routes, Route{
			ID: int64(i/2 + 1), Position: i / 2, Pattern: pairs[i], Destination: pairs[i+1],
		})
	}
	return l
}

func TestCompiledLinkMatchLiterals(t *testing.T) {
	l := link("gh",
		"gh", "https://github.com/acme",
		"gh/new", "https://github.com/new",
		// Not something Ogre would save; such routes must simply never match.
		"", "https://example.com/empty",
		"gh/{", "https://example.com/unclosed",
		"gh/x", "",
	)
	c := Compile(l)

	tests := []struct {
		segments []string
		wantID   int64 // 0 means no route matches
	}{
		{[]string{"gh"}, 1},
		{[]string{"GH"}, 1},
		{[]string{"gh", "New"}, 2},
		{[]string{"gh", "old"}, 0},
		{[]string{"gh", "{"}, 0},
		{[]string{"gh", "x"}, 0},
		{[]string{"gh", "new", "x"}, 0},
		{[]string{""}, 0},
		{nil, 0},
	}
	for _, tt := range tests {
		m, ok, reasons, err := c.Match(tt.segments)
		if err != nil {
			t.Fatalf("Match(%q): %v", tt.segments, err)
		}
		if ok != (tt.wantID != 0) || m.Route.ID != tt.wantID {
			t.Errorf("Match(%q) = %+v, %v; want route %d", tt.segments, m, ok, tt.wantID)
		}
		if ok {
			want := l.Routes[tt.wantID-1]
			if !reflect.DeepEqual(m.Route, want) || m.Destination != want.Destination || m.Values != nil || reasons != nil {
				t.Errorf("Match(%q) = %+v, reasons %+v; want the stored route %+v and nothing else", tt.segments, m, reasons, want)
			}
		} else if len(reasons) != len(l.Routes) || !reflect.DeepEqual(m, Match{}) {
			t.Errorf("Match(%q) = %+v with %d reasons; want the zero Match and one reason per route", tt.segments, m, len(reasons))
		}
	}

	m, ok, reasons, err := Compile(Link{Name: "empty"}).Match([]string{"empty"})
	if ok || reasons != nil || err != nil {
		t.Errorf("a link without routes: Match = %+v, %v, %+v, %v", m, ok, reasons, err)
	}
}

// PRD 5.5, step 4: of several matching routes the most specific wins,
// compared from the left; a tie goes to the route listed first.
func TestMatchSpecificity(t *testing.T) {
	const d = "https://example.com/"
	tests := []struct {
		name    string
		routes  []string // patterns, in route order
		request string
		want    string // the pattern that must win; "" for a miss
	}{
		{"literal beats text", []string{"gh/{repo}", "gh/new"}, "gh/new", "gh/new"},
		{"literal beats text, listed first", []string{"gh/new", "gh/{repo}"}, "gh/new", "gh/new"},
		{"literal beats restricted", []string{"v/{n:int}", "v/1"}, "v/1", "v/1"},
		{"restricted beats text", []string{"gh/{repo}", "gh/{id:int}"}, "gh/42", "gh/{id:int}"},
		{"restricted beats text, listed first", []string{"gh/{id:int}", "gh/{repo}"}, "gh/42", "gh/{id:int}"},
		{"text takes what the restricted one refuses", []string{"gh/{id:int}", "gh/{repo}"}, "gh/api", "gh/{repo}"},
		{"number beats text", []string{"x/{a}", "x/{n:number}"}, "x/3.5", "x/{n:number}"},
		{"regex beats text", []string{"x/{a}", "x/{k:regex([A-Z]+)}"}, "x/OPS", "x/{k:regex([A-Z]+)}"},
		{"all three kinds", []string{"x/{a}", "x/{n:int}", "x/7"}, "x/7", "x/7"},
		{"all three kinds, literal missing its value", []string{"x/{a}", "x/{n:int}", "x/7"}, "x/8", "x/{n:int}"},
		{"all three kinds, only text fits", []string{"x/{a}", "x/{n:int}", "x/7"}, "x/y", "x/{a}"},

		// The first difference from the left decides, however specific the
		// rest is.
		{"left literal beats right literal", []string{"x/{a}/b", "x/a/{b}"}, "x/a/b", "x/a/{b}"},
		{"left literal beats right literal, other order", []string{"x/a/{b}", "x/{a}/b"}, "x/a/b", "x/a/{b}"},
		{"left restricted beats right restricted", []string{"x/{a}/{b:int}", "x/{a:int}/{b}"}, "x/1/2", "x/{a:int}/{b}"},
		{"one early literal beats two late ones", []string{"x/{a}/b/c", "x/a/{b}/{c}"}, "x/a/b/c", "x/a/{b}/{c}"},
		{"equal start, decided later", []string{"x/a/{b}", "x/a/{b:int}"}, "x/a/5", "x/a/{b:int}"},
		{"equal start, decided by a literal later", []string{"x/{a}/{b}", "x/{a}/b"}, "x/q/b", "x/{a}/b"},

		// Equally specific: route order.
		{"int and number tie, first wins", []string{"x/{n:number}", "x/{n:int}"}, "x/5", "x/{n:number}"},
		{"int and number tie, other order", []string{"x/{n:int}", "x/{n:number}"}, "x/5", "x/{n:int}"},
		{"two expressions tie, first wins", []string{"x/{a:regex([0-9]+)}", "x/{b:regex([0-9a-f]+)}"}, "x/12", "x/{a:regex([0-9]+)}"},
		{"two expressions, only the second fits", []string{"x/{a:regex([0-9]+)}", "x/{b:regex([0-9a-f]+)}"}, "x/ff", "x/{b:regex([0-9a-f]+)}"},
		{"int and regex tie, first wins", []string{"x/{a:regex(.+)}", "x/{n:int}"}, "x/5", "x/{a:regex(.+)}"},

		// Routes of different lengths never compete.
		{"shorter", []string{"gh", "gh/{repo}", "gh/{repo}/pr/{num:int}"}, "gh", "gh"},
		{"middle", []string{"gh", "gh/{repo}", "gh/{repo}/pr/{num:int}"}, "gh/x", "gh/{repo}"},
		{"longer", []string{"gh", "gh/{repo}", "gh/{repo}/pr/{num:int}"}, "gh/x/pr/1", "gh/{repo}/pr/{num:int}"},
		{"no length fits", []string{"gh", "gh/{repo}", "gh/{repo}/pr/{num:int}"}, "gh/x/pr", ""},
		{"nothing fits", []string{"x/{n:int}", "x/a"}, "x/b", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pairs []string
			for _, p := range tt.routes {
				pairs = append(pairs, p, d)
			}
			m, ok, _, err := Compile(link(strings.Split(tt.request, "/")[0], pairs...)).Match(strings.Split(tt.request, "/"))
			if err != nil {
				t.Fatal(err)
			}
			if ok != (tt.want != "") || m.Route.Pattern != tt.want {
				t.Errorf("%q against %q matched %q (%v), want %q", tt.request, tt.routes, m.Route.Pattern, ok, tt.want)
			}
		})
	}
}

// A tie goes to the lower Position, which is the owner's order, whatever
// order the routes happen to be held in.
func TestMatchTieUsesPosition(t *testing.T) {
	l := Link{Name: "x", Routes: []Route{
		{ID: 1, Position: 2, Pattern: "x/{a:regex([0-9]+)}", Destination: "https://example.com/a"},
		{ID: 2, Position: 0, Pattern: "x/{b:regex([0-9a-f]+)}", Destination: "https://example.com/b"},
		{ID: 3, Position: 1, Pattern: "x/{c:int}", Destination: "https://example.com/c"},
		{ID: 4, Position: 3, Pattern: "x/{d}", Destination: "https://example.com/d"},
	}}
	m, ok, _, err := Compile(l).Match([]string{"x", "12"})
	if !ok || err != nil || m.Route.ID != 2 {
		t.Errorf("Match = route %d (%v, %v), want route 2, the one with the lowest position", m.Route.ID, ok, err)
	}
	// Specificity still comes before position.
	l.Routes[3].Position = -1
	if m, _, _, _ := Compile(l).Match([]string{"x", "12"}); m.Route.ID != 2 {
		t.Errorf("a text variable with a lower position won: route %d", m.Route.ID)
	}
}

func TestMatchValues(t *testing.T) {
	c := Compile(link("x",
		"x/{a}/lit/{b:int}/{c:regex(.+)}", "https://example.com/{a}?b={b}&c={c}",
		"x", "https://example.com/",
	))
	// Values are exactly what was typed: no trimming, no case change, no
	// reformatting (PRD 5.3).
	segments := []string{"X", " Mixed Case ", "LIT", "-007", "a/b?c#d"}
	m, ok, _, err := c.Match(segments)
	if !ok || err != nil {
		t.Fatalf("Match = %v, %v", ok, err)
	}
	want := map[string]string{"a": " Mixed Case ", "b": "-007", "c": "a/b?c#d"}
	if !reflect.DeepEqual(m.Values, want) {
		t.Errorf("Values = %q, want %q", m.Values, want)
	}
	if got, want := m.Destination, "https://example.com/%20Mixed%20Case%20?b=-007&c=a%2Fb%3Fc%23d"; got != want {
		t.Errorf("Destination = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(segments, []string{"X", " Mixed Case ", "LIT", "-007", "a/b?c#d"}) {
		t.Errorf("Match changed the request segments: %q", segments)
	}
	// Each call gets its own map: one request cannot see another's values.
	other, _, _, _ := c.Match([]string{"x", "p", "lit", "1", "q"})
	if m.Values["a"] != " Mixed Case " || other.Values["a"] != "p" {
		t.Errorf("two matches share their values: %q and %q", m.Values, other.Values)
	}
	if m, ok, _, _ := c.Match([]string{"x"}); !ok || m.Values != nil {
		t.Errorf("a route without variables: ok %v, Values %v; want nil", ok, m.Values)
	}
}

// PRD 5.6 and MISS-2: for each route, the reason this request did not fit.
func TestMatchReasons(t *testing.T) {
	c := Compile(link("gh",
		"gh", "https://github.com/acme",
		"gh/new", "https://github.com/new",
		"gh/{id:int}", "https://github.com/acme/issues/{id}",
		"gh/{repo}/pr/{num:int}", "https://github.com/acme/{repo}/pull/{num}",
		"gh/{team}/{key:regex([A-Z]+-[0-9]+)}", "https://example.com/{team}?k={key}",
		"gh/{a}/{b}/{c}/{d}", "https://example.com/?a={a}&b={b}&c={c}&d={d}",
	))
	type want struct {
		code, variable, value, accepts, message string
	}
	count := func(message string) want { return want{code: ReasonSegmentCount, message: message} }
	tests := []struct {
		name     string
		segments []string
		want     []want
	}{
		{"a value that fails its type", []string{"gh", "latest"}, []want{
			count(`This route takes nothing after "gh", and the request has 1.`),
			{ReasonLiteralMismatch, "", "latest", "new", `The request has "latest" where this route expects "new".`},
			{ReasonValueRejected, "id", "latest", "a whole number", `id doesn't accept "latest". It accepts a whole number.`},
			count(`This route needs 3 parts after "gh", and the request has 1.`),
			count(`This route needs 2 values after "gh", and the request has 1.`),
			count(`This route needs 4 values after "gh", and the request has 1.`),
		}},
		{"a longer request", []string{"GH", "api", "pr", "latest"}, []want{
			count(`This route takes nothing after "gh", and the request has 3.`),
			count(`This route needs 1 part after "gh", and the request has 3.`),
			count(`This route needs 1 value after "gh", and the request has 3.`),
			{ReasonValueRejected, "num", "latest", "a whole number", `num doesn't accept "latest". It accepts a whole number.`},
			count(`This route needs 2 values after "gh", and the request has 3.`),
			count(`This route needs 4 values after "gh", and the request has 3.`),
		}},
		{"the first failure from the left is the reason", []string{"gh", "api", "pull", "x"}, []want{
			count(`This route takes nothing after "gh", and the request has 3.`),
			count(`This route needs 1 part after "gh", and the request has 3.`),
			count(`This route needs 1 value after "gh", and the request has 3.`),
			{ReasonLiteralMismatch, "", "pull", "pr", `The request has "pull" where this route expects "pr".`},
			count(`This route needs 2 values after "gh", and the request has 3.`),
			count(`This route needs 4 values after "gh", and the request has 3.`),
		}},
		{"far too long", []string{"gh", "a", "b", "c", "d", "e"}, []want{
			count(`This route takes nothing after "gh", and the request has 5.`),
			count(`This route needs 1 part after "gh", and the request has 5.`),
			count(`This route needs 1 value after "gh", and the request has 5.`),
			count(`This route needs 3 parts after "gh", and the request has 5.`),
			count(`This route needs 2 values after "gh", and the request has 5.`),
			count(`This route needs 4 values after "gh", and the request has 5.`),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok, reasons, err := c.Match(tt.segments)
			if ok || err != nil || !reflect.DeepEqual(m, Match{}) {
				t.Fatalf("Match(%q) = %+v, %v, %v; want a miss", tt.segments, m, ok, err)
			}
			if len(reasons) != len(tt.want) {
				t.Fatalf("got %d reasons, want %d: %+v", len(reasons), len(tt.want), reasons)
			}
			for i, w := range tt.want {
				r, route := reasons[i], c.Link.Routes[i]
				got := want{r.Code, r.Variable, r.Value, r.Accepts, r.Message}
				if got != w || r.RouteID != route.ID || r.Pattern != route.Pattern {
					t.Errorf("reason %d (route %d %q) =\n  %+v\nwant\n  %+v", i, r.RouteID, r.Pattern, got, w)
				}
			}
		})
	}

	// One reason for a regex route: the plan's example sentence.
	reports := Compile(link("reports", "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "https://example.com/?m={month}&d={day}"))
	_, _, reasons, _ := reports.Match([]string{"reports", "13", "01"})
	if got, want := reasons[0].Message, `month doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$.`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	// Singular and plural, and a request for the bare name.
	_, _, reasons, _ = reports.Match([]string{"reports"})
	if got, want := reasons[0].Message, `This route needs 2 values after "reports", and the request has none.`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// The request's text goes into a reason as it is, but the sentence quotes it
// and cuts it short: a value is whatever a URL contained.
func TestMatchReasonQuotesTheValue(t *testing.T) {
	c := Compile(link("x", "x/{n:int}", "https://example.com/?n={n}", "x/lit", "https://example.com/"))
	long := strings.Repeat("ż", 5000)
	for _, value := range []string{"a\r\nb", "a\"b", "\x00", "a\xffb", "\u202egpj", long, "<script>"} {
		_, ok, reasons, _ := c.Match([]string{"x", value})
		if ok || len(reasons) != 2 {
			t.Fatalf("Match with %q: ok %v, %d reasons", value, ok, len(reasons))
		}
		for _, r := range reasons {
			if r.Value != value {
				t.Errorf("reason value = %q, want the request's text unchanged", r.Value)
			}
			if strings.ContainsAny(r.Message, "\r\n\x00\u202e") || !strings.HasSuffix(r.Message, ".") {
				t.Errorf("message %q carries a raw control character", r.Message)
			}
			if len(r.Message) > 600 {
				t.Errorf("message is %d bytes long for a value of %d", len(r.Message), len(value))
			}
		}
	}
}

// NFR-10: "." and ".." are refused for a variable whose value goes into the
// destination's path, where a browser would resolve them as path steps. In a
// query or a fragment they are harmless and allowed.
func TestMatchDotSegments(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		dest    string
		request []string
		wantOK  bool
		wantVar string // the variable named by the reason, on a miss
	}{
		{"dot dot in the path", "x/{v}", "https://example.com/a/{v}", []string{"x", ".."}, false, "v"},
		{"dot in the path", "x/{v}", "https://example.com/a/{v}", []string{"x", "."}, false, "v"},
		{"dot dot in the middle of the path", "x/{v}", "https://example.com/a/{v}/b", []string{"x", ".."}, false, "v"},
		{"dot dot as part of a segment", "x/{v}", "https://example.com/a/{v}.html", []string{"x", ".."}, false, "v"},
		{"dot next to a literal dot", "x/{v}", "https://example.com/a/.{v}", []string{"x", "."}, false, "v"},
		{"in the path and the query", "x/{v}", "https://example.com/{v}?q={v}", []string{"x", ".."}, false, "v"},
		{"two variables making a dot segment", "x/{a}/{b}", "https://example.com/{a}{b}", []string{"x", ".", "."}, false, "a"},
		{"second variable in the path", "x/{a}/{b}", "https://example.com/?a={a}#x/{b}", []string{"x", "..", ".."}, true, ""},
		{"only the second is in the path", "x/{a}/{b}", "https://example.com/{b}?a={a}", []string{"x", "..", ".."}, false, "b"},
		{"regex that would accept dots", "x/{v:regex(.+)}", "https://example.com/{v}", []string{"x", ".."}, false, "v"},

		{"dot dot in the query", "x/{v}", "https://example.com/?q={v}", []string{"x", ".."}, true, ""},
		{"dot in the fragment", "x/{v}", "https://example.com/#{v}", []string{"x", "."}, true, ""},
		{"unused variable", "x/{v}", "https://example.com/", []string{"x", ".."}, true, ""},
		{"three dots are a name", "x/{v}", "https://example.com/{v}", []string{"x", "..."}, true, ""},
		{"dots with more", "x/{v}", "https://example.com/{v}", []string{"x", "..a"}, true, ""},
		{"a hidden file", "x/{v}", "https://example.com/{v}", []string{"x", ".git"}, true, ""},
		{"dots inside a name", "x/{v}", "https://example.com/{v}", []string{"x", "v1..2"}, true, ""},
		{"a name ending in dots between slashes", "x/{v}", "https://example.com/{v}", []string{"x", "a../b"}, true, ""},
		{"a slash and no dots", "x/{v}", "https://example.com/{v}", []string{"x", "a/b"}, true, ""},

		// A value that came with an encoded slash is sent on with the slash
		// encoded again ("..%2Fsecret"), which is one segment to a browser.
		// A server that decodes before it normalises reads a step up all the
		// same, so a value with "." or ".." between slashes is refused like
		// the bare ones (NFR-10).
		{"dot dot, slash, name", "x/{v}", "https://example.com/a/{v}", []string{"x", "../secret"}, false, "v"},
		{"dot dot twice", "x/{v}", "https://example.com/a/{v}", []string{"x", "../.."}, false, "v"},
		{"name, slash, dot dot", "x/{v}", "https://example.com/a/{v}", []string{"x", "b/.."}, false, "v"},
		{"dot dot in the middle", "x/{v}", "https://example.com/a/{v}", []string{"x", "a/../b"}, false, "v"},
		{"a single dot step", "x/{v}", "https://example.com/a/{v}", []string{"x", "a/./b"}, false, "v"},
		{"dot dot, backslash", "x/{v}", "https://example.com/a/{v}", []string{"x", `..\secret`}, false, "v"},
		{"backslash, dot dot", "x/{v}", "https://example.com/a/{v}", []string{"x", `a\..`}, false, "v"},
		{"an empty step and dot dot", "x/{v}", "https://example.com/a/{v}", []string{"x", "/.."}, false, "v"},
		{"a regex that accepts it", "x/{v:regex(.+)}", "https://example.com/a/{v}", []string{"x", "../x"}, false, "v"},
		{"dot dot slash in the query", "x/{v}", "https://example.com/?q={v}", []string{"x", "../secret"}, true, ""},
		{"dot dot slash in the fragment", "x/{v}", "https://example.com/#{v}", []string{"x", "a/../b"}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok, reasons, err := Compile(link("x", tt.pattern, tt.dest)).Match(tt.request)
			if err != nil || ok != tt.wantOK {
				t.Fatalf("Match(%q) = %+v, %v, %v; want ok = %v", tt.request, m, ok, err, tt.wantOK)
			}
			if ok {
				checkExpanded(t, tt.dest, m.Destination)
				return
			}
			r := reasons[0]
			if r.Code != ReasonValueNotAllowed || r.Variable != tt.wantVar || !strings.Contains(r.Message, "destination's path") {
				t.Errorf("reason = %+v, want %s for %q", r, ReasonValueNotAllowed, tt.wantVar)
			}
		})
	}
}

// Whatever a request carries, a hit lands on the host the owner wrote.
func TestMatchCannotLeaveTheHost(t *testing.T) {
	routes := [][2]string{
		{"x/{v}", "https://example.com/{v}"},
		{"x/{v}", "https://example.com/a/{v}/b?c=d#e"},
		{"x/{v}", "https://example.com/?next={v}"},
		{"x/{v}", "https://example.com/#{v}"},
		{"x/{v}", "https://example.com:8443/{v}{v}"},
		{"x/{v}", "http://[::1]:9000/{v}"},
	}
	values := []string{
		"@evil.example", "//evil.example", "/\\evil.example", "\\\\evil.example", "https://evil.example", "evil.example:80",
		".evil.example", "a@b", "?x", "#x", "%2f%2fevil.example", "\r\nLocation: https://evil.example", "\t", " ",
		"..%2f..", ";@evil.example", "%00", "\x00", "a\u2028b", "http:evil.example", "javascript:alert(1)",
	}
	for _, r := range routes {
		c := Compile(link("x", r[0], r[1]))
		if !c.routes[0].valid {
			t.Fatalf("route %q did not compile", r[1])
		}
		for _, v := range values {
			m, ok, _, err := c.Match([]string{"x", v})
			if err != nil {
				t.Errorf("%q with %q: %v", r[1], v, err)
				continue
			}
			if ok {
				checkExpanded(t, r[1], m.Destination)
			}
		}
	}
}

// A route whose stored text cannot be read never matches, and says so.
func TestMatchInvalidRoutes(t *testing.T) {
	c := Compile(link("x",
		"x/{", "https://example.com/",
		"x/{v}", "https://example.com/{other}",
		"x/{v}", "https://{v}.example.com/",
		"x/{v:date}", "https://example.com/{v}",
		"x/{v:int}", "https://example.com/{v}",
	))
	_, ok, reasons, err := c.Match([]string{"x", "abc"})
	if ok || err != nil || len(reasons) != 5 {
		t.Fatalf("Match = %v, %v, %d reasons", ok, err, len(reasons))
	}
	for i, r := range reasons[:4] {
		if r.Code != ReasonRouteInvalid || !strings.Contains(r.Message, "can't be read") || r.RouteID != int64(i+1) {
			t.Errorf("reason %d = %+v, want %s", i, r, ReasonRouteInvalid)
		}
	}
	if reasons[4].Code != ReasonValueRejected {
		t.Errorf("the valid route's reason = %+v", reasons[4])
	}
	if m, ok, _, _ := c.Match([]string{"x", "7"}); !ok || m.Route.ID != 5 {
		t.Errorf("the valid route did not answer: %+v, %v", m, ok)
	}
}

// When the destination does not expand to its own scheme and host, Match
// reports an error and no destination: the caller answers 500, it does not
// redirect and it does not treat the request as a miss.
func TestMatchExpandFailure(t *testing.T) {
	c := Compile(link("x", "x/{v}", "https://example.com/{v}"))
	// Damage the template the way only a bug could.
	c.routes[0].template.host = "other.example"

	m, ok, reasons, err := c.Match([]string{"x", "secret-value"})
	if !errors.Is(err, ErrExpand) {
		t.Fatalf("Match err = %v, want an ErrExpand", err)
	}
	if ok || reasons != nil || !reflect.DeepEqual(m, Match{}) {
		t.Errorf("Match = %+v, %v, %+v alongside the error; want nothing", m, ok, reasons)
	}
	// The error is logged: it names the link and the route, never the value.
	if msg := err.Error(); !strings.Contains(msg, `"x"`) || !strings.Contains(msg, "route 1") || strings.Contains(msg, "secret-value") {
		t.Errorf("error = %q", msg)
	}
}

// Matching runs on every request. For a route without variables it must not
// allocate.
func TestMatchDoesNotAllocate(t *testing.T) {
	c := Compile(link("gh",
		"gh", "https://github.com/acme",
		"gh/acme/pulls/new", "https://github.com/acme/pulls/new",
		"gh/{a}/pulls/{b:int}", "https://github.com/{a}/pulls/{b}",
	))
	segments := []string{"GH", "Acme", "Pulls", "New"}
	if _, ok, _, _ := c.Match(segments); !ok {
		t.Fatal("the request did not match")
	}
	if allocs := testing.AllocsPerRun(100, func() { c.Match(segments) }); allocs != 0 {
		t.Errorf("Match allocated %v times per call, want 0", allocs)
	}
}

func TestAccepts(t *testing.T) {
	pattern := mustParse(t, "x/{id:int}/{name}/{q}")
	tmpl, problems := ParseDestination("https://example.com/{id}/{name}?q={q}", pattern)
	if problems != nil {
		t.Fatal(problems)
	}
	tests := []struct {
		variable, value    string
		accepted, declared bool
	}{
		{"id", "42", true, true},
		{"id", "x", false, true},
		{"id", "", false, true},
		{"id", "..", false, true},
		{"name", "api", true, true},
		{"name", "..", false, true}, // in the destination's path
		{"name", ".", false, true},
		{"q", "..", true, true}, // in the query
		{"q", "", false, true},
		{"other", "1", false, false},
		{"ID", "1", false, false},
		{"", "1", false, false},
	}
	for _, tt := range tests {
		accepted, declared := Accepts(pattern, tmpl, tt.variable, tt.value)
		if accepted != tt.accepted || declared != tt.declared {
			t.Errorf("Accepts(%q, %q) = %v, %v; want %v, %v", tt.variable, tt.value, accepted, declared, tt.accepted, tt.declared)
		}
	}
	// Without a usable destination only the type is asked.
	if accepted, declared := Accepts(pattern, Template{}, "name", ".."); !accepted || !declared {
		t.Errorf("Accepts with the zero Template = %v, %v; want true, true", accepted, declared)
	}
	if accepted, declared := Accepts(Pattern{}, tmpl, "name", "x"); accepted || declared {
		t.Errorf("Accepts with the zero Pattern = %v, %v; want false, false", accepted, declared)
	}
}

// Accepts and Match are one rule: a sample the validation endpoint calls
// accepted is a value the resolver takes, and the other way round.
func TestAcceptsAgreesWithMatch(t *testing.T) {
	routes := [][2]string{
		{"x/{v}", "https://example.com/{v}"},
		{"x/{v}", "https://example.com/?q={v}"},
		{"x/{v:int}", "https://example.com/{v}"},
		{"x/{v:number}", "https://example.com/#{v}"},
		{"x/{v:regex(\\.+)}", "https://example.com/{v}"},
		{"x/{v:regex(\\.+)}", "https://example.com/?q={v}"},
	}
	values := []string{"", ".", "..", "...", "1", "-1", "3.5", "a", "A b", "/"}
	for _, r := range routes {
		pattern := mustParse(t, r[0])
		tmpl, problems := ParseDestination(r[1], pattern)
		if problems != nil {
			t.Fatal(problems)
		}
		c := Compile(link("x", r[0], r[1]))
		for _, v := range values {
			accepted, _ := Accepts(pattern, tmpl, "v", v)
			if _, ok, _, _ := c.Match([]string{"x", v}); ok != accepted {
				t.Errorf("route %q -> %q, value %q: Accepts = %v, Match = %v", r[0], r[1], v, accepted, ok)
			}
		}
	}
}

// Compile keeps the link as it was given: the routes are shared, not copied
// or reordered.
func TestCompileKeepsTheLink(t *testing.T) {
	l := link("gh", "gh/{repo}", "https://github.com/acme/{repo}", "gh", "https://github.com/acme")
	l.Description = "GitHub"
	l.Routes[0].Variables = []VariableMeta{{Name: "repo", Description: "Repository", Example: "api-server"}}
	c := Compile(l)
	if !reflect.DeepEqual(c.Link, l) {
		t.Errorf("Compile changed the link:\n  %+v\nwant\n  %+v", c.Link, l)
	}
	m, ok, _, _ := c.Match([]string{"gh", "x"})
	if !ok || !reflect.DeepEqual(m.Route, l.Routes[0]) {
		t.Errorf("Match returned route %+v, want the stored route with its variables %+v", m.Route, l.Routes[0])
	}
}

// countingType is a variable type that counts how often it is asked.
type countingType struct {
	VarType
	asked *atomic.Int64
}

func (c countingType) Accepts(v string) bool {
	c.asked.Add(1)
	return c.VarType.Accepts(v)
}

// counted returns the compiled link with every variable's type wrapped so
// that asked counts the calls of Accepts.
func counted(c CompiledLink, asked *atomic.Int64) CompiledLink {
	for i := range c.routes {
		segments := slices.Clone(c.routes[i].pattern.segments)
		for j := range segments {
			if segments[j].Type != nil {
				segments[j].Type = countingType{segments[j].Type, asked}
			}
		}
		c.routes[i].pattern.segments = segments
	}
	return c
}

// Checking a value is the costly part of matching when the variable is a
// regular expression, and a miss is what anyone can ask for at will. So a
// request asks each variable about its value at most once, hit or miss:
// explaining a miss reuses what the search found (NFR-8).
func TestMatchChecksEachValueOnce(t *testing.T) {
	l := link("x",
		"x/{a:int}/{b:regex([a-z]+)}", "https://example.com/{a}/{b}",
		"x/{a:regex([0-9]+)}/{b:int}", "https://example.com/?a={a}&b={b}",
		"x/{a}/{b}/{c}", "https://example.com/{a}/{b}/{c}",
		"x/{a:number}", "https://example.com/{a}",
	)
	tests := []struct {
		name    string
		request []string
		wantOK  bool
		want    int64 // calls of Accepts
	}{
		// Route 1: a, b. Route 2: a, b. Routes 3 and 4: the wrong length.
		{"a hit", []string{"x", "7", "abc"}, true, 4},
		{"a miss on the last value of each route", []string{"x", "7", "A-1"}, false, 4},
		{"a miss on the first value of each route", []string{"x", "?", "abc"}, false, 2},
		{"a miss that is refused for its dots", []string{"x", "7", ".."}, false, 4},
		{"the wrong number of segments", []string{"x", "1", "2", "3", "4"}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked atomic.Int64
			_, ok, reasons, err := counted(Compile(l), &asked).Match(tt.request)
			if err != nil || ok != tt.wantOK {
				t.Fatalf("Match(%q) ok = %v, err = %v; want ok = %v", tt.request, ok, err, tt.wantOK)
			}
			if !ok && len(reasons) != len(l.Routes) {
				t.Fatalf("%d reasons for %d routes", len(reasons), len(l.Routes))
			}
			if got := asked.Load(); got != tt.want {
				t.Errorf("Match(%q) asked the variables %d times, want %d: a value is checked once", tt.request, got, tt.want)
			}
		})
	}
}

// worstRegexLink is a link whose regular expressions use the whole budget of
// MaxLinkRegexProgram, each of a kind that keeps the engine as busy as it
// can be, with the request that makes every one of them run to the end of
// the longest value that is checked. Nothing that can be saved costs more to
// resolve.
func worstRegexLink(tb testing.TB) (CompiledLink, []string) {
	tb.Helper()
	var d Draft
	for i := range 4 {
		d.Routes = append(d.Routes, DraftRoute{
			// Every repeat keeps its own thread alive at every byte of the
			// value, and the "b" that never comes makes the match fail last.
			Pattern:     fmt.Sprintf("slow/{v:regex((.*){1,%d}b)}", 196-i),
			Destination: "https://example.com/?v={v}",
		})
	}
	saved, problems := ValidateDraft(d)
	if problems.Count() != 0 {
		tb.Fatalf("the worst link does not validate: %+v", problems)
	}
	total := 0
	for _, r := range d.Routes {
		p, _ := ParsePattern(r.Pattern)
		total += regexProgram(p)
	}
	if total > MaxLinkRegexProgram || total < MaxLinkRegexProgram*95/100 {
		tb.Fatalf("the worst link uses %d instructions of %d; it must use nearly all", total, MaxLinkRegexProgram)
	}
	// A little more and the link is refused: this is the limit.
	more := Draft{Routes: append(slices.Clone(d.Routes), DraftRoute{
		Pattern: "slow/{v:regex((.*){1,40}c)}", Destination: "https://example.com/?v={v}"})}
	if _, problems := ValidateDraft(more); len(problems.Link) != 1 || problems.Link[0].Code != CodePatternRegexTooComplex {
		tb.Fatalf("a link over the budget was accepted: %+v", problems)
	}
	return Compile(saved), []string{"slow", strings.Repeat("a", MaxRegexValueLength)}
}

// NFR-8: no expression can stall the resolver. Linear time alone does not
// say that; the bound on the whole link does. The worst link that can be
// saved, asked the worst request, answers in milliseconds (about 10 ms on a
// 2023 laptop; it was 1.6 s to 5 s before the limits were tightened and the
// values were checked once). The ceiling is far above that, so that the race
// detector and a busy machine cannot fail it, and far below a stall.
func TestWorstRegexLinkIsBounded(t *testing.T) {
	// The bound itself, in steps of the engine: every instruction of every
	// expression of a link, at every byte of the longest value looked at.
	// Raising either limit raises what a single request can cost; this is
	// where that has to be decided on purpose.
	if steps := MaxLinkRegexProgram * MaxRegexValueLength; steps > 1<<20 {
		t.Errorf("one request can cost %d engine steps, want at most %d (about 10 ms)", steps, 1<<20)
	}
	if MaxRegexProgram > MaxLinkRegexProgram {
		t.Errorf("one expression may be larger (%d) than all of a link's together (%d)", MaxRegexProgram, MaxLinkRegexProgram)
	}
	c, request := worstRegexLink(t)
	best := time.Hour
	for range 3 {
		start := time.Now()
		_, ok, reasons, err := c.Match(request)
		if took := time.Since(start); took < best {
			best = took
		}
		if ok || err != nil || len(reasons) != 4 || reasons[0].Code != ReasonValueRejected {
			t.Fatalf("Match = ok %v, err %v, reasons %+v; want four refused values", ok, err, reasons)
		}
	}
	ceiling := 250 * time.Millisecond
	if raceEnabled {
		ceiling = 2 * time.Second // the detector alone costs a factor of ten or more
	}
	if best > ceiling {
		t.Errorf("the worst request took %v, want well under %v", best, ceiling)
	}
	// A longer value is not looked at by any expression.
	request[1] += "a"
	start := time.Now()
	if _, ok, _, _ := c.Match(request); ok {
		t.Error("a value over MaxRegexValueLength matched")
	}
	if took := time.Since(start); took > ceiling/10 {
		t.Errorf("a value that is too long took %v to refuse", took)
	}
}

// BenchmarkMatchWorstRegexLink is the cost of the most expensive request
// there can be: see worstRegexLink.
func BenchmarkMatchWorstRegexLink(b *testing.B) {
	c, request := worstRegexLink(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok, _, _ := c.Match(request); ok {
			b.Fatal("the request matched")
		}
	}
}

// A reason names the variable in the owner's words when there are any: the
// description and the example exist to explain a variable to people
// (MISS-2). Without them the sentence is as before.
func TestReasonUsesDescriptionAndExample(t *testing.T) {
	const pattern, dest = "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "https://example.com/{month}/{day}"
	tests := []struct {
		name    string
		meta    []VariableMeta
		request []string
		want    string
	}{
		{"nothing stored", nil, []string{"reports", "13", "1"},
			`month doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$.`},
		{"description and example", []VariableMeta{{Name: "month", Description: "Month, 1 to 12", Example: "10"}}, []string{"reports", "13", "1"},
			`month (Month, 1 to 12) doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$. For example: "10".`},
		{"example only", []VariableMeta{{Name: "month"}, {Name: "day", Example: "13"}}, []string{"reports", "10", "x"},
			`day doesn't accept "x". It accepts a whole number. For example: "13".`},
		{"the other variable's words are not used", []VariableMeta{{Name: "day", Description: "Day of the month", Example: "13"}}, []string{"reports", "13", "1"},
			`month doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$.`},
		{"a value refused for its dots", []VariableMeta{{Name: "month", Description: "Month"}}, []string{"reports", "10", "1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := link("reports", pattern, dest)
			l.Routes[0].Variables = tt.meta
			_, ok, reasons, err := Compile(l).Match(tt.request)
			if tt.want == "" {
				if !ok || err != nil {
					t.Fatalf("Match(%q) = ok %v, err %v; want a hit", tt.request, ok, err)
				}
				return
			}
			if ok || len(reasons) != 1 || reasons[0].Message != tt.want {
				t.Errorf("Match(%q): ok %v, reasons %+v\nwant message %s", tt.request, ok, reasons, tt.want)
			}
			// The fields a client keys on stay bare.
			if len(reasons) == 1 && strings.ContainsAny(reasons[0].Variable+reasons[0].Accepts, "()\"") &&
				!strings.Contains(reasons[0].Accepts, "^(0?") {
				t.Errorf("reason fields carry the description: %+v", reasons[0])
			}
		})
	}
	// The two sentences for a value with a dot step.
	l := link("docs", "docs/{p}", "https://example.com/v1/{p}")
	l.Routes[0].Variables = []VariableMeta{{Name: "p", Description: "Page", Example: "intro"}}
	for request, want := range map[string]string{
		"..":        `p (Page) can't be "..", because its value becomes part of the destination's path. For example: "intro".`,
		"../secret": `p (Page) can't be "../secret": a value that becomes part of the destination's path can't have "." or ".." between slashes. For example: "intro".`,
	} {
		_, ok, reasons, _ := Compile(l).Match([]string{"docs", request})
		if ok || len(reasons) != 1 || reasons[0].Code != ReasonValueNotAllowed || reasons[0].Message != want {
			t.Errorf("Match(docs/%s): ok %v, reasons %+v\nwant %s", request, ok, reasons, want)
		}
	}
}

func TestHasDotStep(t *testing.T) {
	for value, want := range map[string]bool{
		"": false, "a": false, "a/b": false, "...": false, ".a": false, "a.": false, "a..b": false, "a/..b": false, "a../b": false, "/": false,
		".": true, "..": true, "./a": true, "a/.": true, "../a": true, "a/..": true, "a/../b": true, "a/./b": true,
		`..\a`: true, `a\..`: true, `a\..\b`: true, `a/..\b`: true, "/..": true, "../": true, "//..//": true,
	} {
		if got := hasDotStep(value); got != want {
			t.Errorf("hasDotStep(%q) = %v, want %v", value, got, want)
		}
	}
}
