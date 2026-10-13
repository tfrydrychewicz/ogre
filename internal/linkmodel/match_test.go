package linkmodel

import "testing"

func TestPatternMatches(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		segments []string
		want     bool
	}{
		{"single segment", "gh", []string{"gh"}, true},
		{"two segments", "gh/new", []string{"gh", "new"}, true},
		{"request in upper case", "gh/new", []string{"GH", "New"}, true},
		{"pattern not lowercase", "GH/New", []string{"gh", "new"}, true},
		{"separators kept", "on-call/v1.2", []string{"On-Call", "V1.2"}, true},

		{"request is longer", "gh", []string{"gh", "new"}, false},
		{"request is shorter", "gh/new", []string{"gh"}, false},
		{"much longer", "gh", []string{"gh", "a", "b", "c"}, false},
		{"later literal differs", "gh/new", []string{"gh", "old"}, false},
		{"first literal differs", "gh/new", []string{"gl", "new"}, false},
		{"prefix of a literal", "gh/new", []string{"gh", "ne"}, false},
		{"hyphen is not underscore", "on-call", []string{"on_call"}, false},
		{"hyphen is not nothing", "on-call", []string{"oncall"}, false},
		{"a decoded slash is not a separator", "gh/new", []string{"gh/new"}, false},
		{"dot segment", "gh/new", []string{"gh", "."}, false},
		{"unicode case is not folded", "k", []string{"\u212a"}, false}, // KELVIN SIGN

		{"no request segments", "gh", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, problems := ParsePattern(tt.pattern)
			if len(problems) > 0 {
				t.Fatalf("ParsePattern(%q) problems = %+v", tt.pattern, problems)
			}
			if got := p.Matches(tt.segments); got != tt.want {
				t.Errorf("pattern %q Matches(%q) = %v, want %v", tt.pattern, tt.segments, got, tt.want)
			}
		})
	}
}

func TestZeroPatternMatchesNothing(t *testing.T) {
	var p Pattern
	for _, segments := range [][]string{nil, {}, {""}, {"gh"}} {
		if p.Matches(segments) {
			t.Errorf("the zero Pattern matched %q", segments)
		}
	}
	if p.Name() != "" || p.Canonical() != "" || p.Shape() != "" || len(p.Segments()) != 0 {
		t.Errorf("the zero Pattern is not empty: %+v", p)
	}
}

func TestPatternText(t *testing.T) {
	tests := []struct {
		pattern, name, canonical, shape string
		segments                        int
	}{
		{"roadmap", "roadmap", "roadmap", "roadmap", 1},
		{"GH/New/PR", "gh", "gh/new/pr", "gh/new/pr", 3},
		{"On-Call/v1.2", "on-call", "on-call/v1.2", "on-call/v1.2", 2},
	}
	for _, tt := range tests {
		p, problems := ParsePattern(tt.pattern)
		if len(problems) > 0 {
			t.Fatalf("ParsePattern(%q) problems = %+v", tt.pattern, problems)
		}
		if p.Name() != tt.name || p.Canonical() != tt.canonical || p.Shape() != tt.shape || len(p.Segments()) != tt.segments {
			t.Errorf("ParsePattern(%q): name %q, canonical %q, shape %q, %d segments; want %q, %q, %q, %d",
				tt.pattern, p.Name(), p.Canonical(), p.Shape(), len(p.Segments()), tt.name, tt.canonical, tt.shape, tt.segments)
		}
		for i, seg := range p.Segments() {
			if seg.Kind != SegmentLiteral || seg.Literal != NormalizeName(seg.Literal) || seg.Literal == "" {
				t.Errorf("ParsePattern(%q) segment %d = %+v, want a lowercase literal", tt.pattern, i, seg)
			}
		}
		// The canonical text is a fixed point: it parses to the same pattern.
		again, problems := ParsePattern(p.Canonical())
		if len(problems) > 0 || again.Canonical() != p.Canonical() {
			t.Errorf("canonical text %q re-parsed as %q, %+v", p.Canonical(), again.Canonical(), problems)
		}
		if got := Shape(tt.pattern); got != tt.shape {
			t.Errorf("Shape(%q) = %q, want %q", tt.pattern, got, tt.shape)
		}
	}
}

// A store asks for the shape of whatever text it holds; text that is not a
// pattern must come back unchanged, never panic or collapse to "".
func TestShapeOfUnparseableText(t *testing.T) {
	for _, text := range []string{"", "gh//new", "{x}", "-x", "a b"} {
		if got := Shape(text); got != text {
			t.Errorf("Shape(%q) = %q, want the text unchanged", text, got)
		}
	}
}

func TestInvalidPatternIsZero(t *testing.T) {
	for _, text := range []string{"", "gh//new", "{x}", "favicon.ico", "a b"} {
		p, problems := ParsePattern(text)
		if len(problems) == 0 {
			t.Fatalf("ParsePattern(%q) reported no problem", text)
		}
		if len(p.Segments()) != 0 {
			t.Errorf("ParsePattern(%q) returned segments %+v with problems", text, p.Segments())
		}
	}
}

func TestCompiledLinkMatch(t *testing.T) {
	link := Link{Name: "gh", Routes: []Route{
		{ID: 1, Pattern: "gh", Destination: "https://github.com/acme"},
		{ID: 2, Pattern: "gh/new", Destination: "https://github.com/new"},
		// Not something godir would save; it must simply never match.
		{ID: 3, Pattern: "gh/{repo}", Destination: "https://github.com/acme/x"},
		{ID: 4, Pattern: "", Destination: "https://example.com/empty"},
	}}
	c := Compile(link)

	tests := []struct {
		segments []string
		wantID   int64 // 0 means no route matches
	}{
		{[]string{"gh"}, 1},
		{[]string{"GH"}, 1},
		{[]string{"gh", "New"}, 2},
		{[]string{"gh", "old"}, 0},
		{[]string{"gh", "{repo}"}, 0},
		{[]string{"gh", "new", "x"}, 0},
		{[]string{""}, 0},
		{nil, 0},
	}
	for _, tt := range tests {
		route, ok := c.Match(tt.segments)
		if ok != (tt.wantID != 0) || route.ID != tt.wantID {
			t.Errorf("Match(%q) = %+v, %v; want route %d", tt.segments, route, ok, tt.wantID)
		}
		if ok && route != link.Routes[tt.wantID-1] {
			t.Errorf("Match(%q) returned %+v, want the stored route %+v", tt.segments, route, link.Routes[tt.wantID-1])
		}
	}

	if got, ok := Compile(Link{Name: "empty"}).Match([]string{"empty"}); ok {
		t.Errorf("a link without routes matched: %+v", got)
	}
}

// Matching runs on every request; it must not allocate.
func TestMatchDoesNotAllocate(t *testing.T) {
	c := Compile(Link{Name: "gh", Routes: []Route{
		{Pattern: "gh"}, {Pattern: "gh/acme/pulls/new"},
	}})
	segments := []string{"GH", "Acme", "Pulls", "New"}
	if _, ok := c.Match(segments); !ok {
		t.Fatal("the request did not match")
	}
	if allocs := testing.AllocsPerRun(100, func() { c.Match(segments) }); allocs != 0 {
		t.Errorf("Match allocated %v times per call, want 0", allocs)
	}
}
