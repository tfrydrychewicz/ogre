package linkmodel

import (
	"strings"
	"testing"
	"time"
)

// typeOf returns the type of the variable in "x/{v<suffix>}".
func typeOf(t testing.TB, suffix string) VarType {
	t.Helper()
	return mustParse(t, "x/{v"+suffix+"}").Segments()[1].Type
}

func TestTypeProperties(t *testing.T) {
	tests := []struct {
		suffix     string // what follows the name in the pattern
		name       string
		format     string
		describe   string
		restricted bool
	}{
		{"", "string", "", "any text", false},
		{":string", "string", "", "any text", false},
		{":int", "int", "int", "a whole number", true},
		{":number", "number", "number", "a number such as 42 or 3.5", true},
		{":regex(^(0?[1-9]|1[0-2])$)", "regex", "regex(^(0?[1-9]|1[0-2])$)", "text matching ^(0?[1-9]|1[0-2])$", true},
		{":regex([A-Z]+-[0-9]+)", "regex", "regex([A-Z]+-[0-9]+)", "text matching [A-Z]+-[0-9]+", true},
	}
	for _, tt := range tests {
		typ := typeOf(t, tt.suffix)
		if typ.Name() != tt.name || typ.Format() != tt.format || typ.Describe() != tt.describe || typ.Restricted() != tt.restricted {
			t.Errorf("{v%s}: Name %q, Format %q, Describe %q, Restricted %v; want %q, %q, %q, %v", tt.suffix,
				typ.Name(), typ.Format(), typ.Describe(), typ.Restricted(), tt.name, tt.format, tt.describe, tt.restricted)
		}
		// Describe finishes the sentence "It accepts …".
		if d := typ.Describe(); d == "" || strings.HasSuffix(d, ".") || strings.ToLower(d[:1]) != d[:1] {
			t.Errorf("{v%s}: Describe() = %q does not read as the end of a sentence", tt.suffix, d)
		}
	}
	// The constructors give the same types the parser does.
	for _, typ := range []VarType{StringType(), IntType(), NumberType()} {
		if got := typeOf(t, ":"+typ.Name()); got != typ {
			t.Errorf("the parser's %s type is %#v, the constructor's %#v", typ.Name(), got, typ)
		}
	}
}

func TestTypeAccepts(t *testing.T) {
	huge := strings.Repeat("9", 5000)
	tests := []struct {
		suffix string
		accept []string
		reject []string
	}{
		{"", // text: any non-empty segment, exactly as typed
			[]string{"a", "api-server", "A B", " ", "0", "-", ".", "..", "a/b", "żółć", "日本", "%41", "a\nb", "\x00", "{x}"},
			[]string{""}},
		{":int", // digits with an optional leading "-"
			[]string{"0", "7", "42", "03", "007", "-1", "-0", "-007", huge, "-" + huge},
			[]string{"", "-", "+5", "+0", "--1", "1-", "1.0", "1.", ".1", "1e3", "0x10", " 1", "1 ", "1 2", "1,000", "1_000",
				"x", "12a", "a12", "١٢٣", "１２", "²", "Ⅷ", "-١", "1\n", "\n1", "1\x00", "NaN", "inf"}},
		{":number", // an integer or a decimal such as 3.5
			[]string{"0", "42", "03", "-1", "-0", "3.5", "-3.5", "0.5", "00.50", "3.0", "10.000", "-0.0", huge, huge + "." + huge},
			[]string{"", "-", ".", "-.", "3.", ".5", "-.5", "-3.", "1e3", "1E3", "1.5e3", "+3.5", "3.5.1", "3..5", "1,5", "1 .5",
				"3,5", "0x1p3", "NaN", "Inf", "-Inf", "infinity", "x", "3.5x", "x3.5", " 3.5", "3.5 ", "٣.٥", "３.５", "1.٥", "3.5\n", "--1.5", "1_000.5"}},
		{":regex(^(0?[1-9]|1[0-2])$)", // PRD 5.7: a month
			[]string{"1", "9", "01", "09", "10", "11", "12"},
			[]string{"", "0", "00", "13", "20", "010", "1 ", " 1", "1\n", "\n1", "1\n2", "x", "１"}},
		{":regex([A-Z]+-[0-9]+)", // PRD 5.7: unanchored in the text, anchored in effect
			[]string{"OPS-1234", "A-0"},
			[]string{"", "ops-1234", "OPS-", "-1", "xOPS-1", "OPS-1x", "OPS-1234\n", "a OPS-1 b", "OPS-1/OPS-2"}},
		{":regex((?i)[a-z]+)", // case-insensitive only by opting in
			[]string{"abc", "ABC", "aBc"},
			[]string{"", "abc1", "1abc"}},
		{":regex(a|b)", // alternation is inside the anchors, not split by them
			[]string{"a", "b"},
			[]string{"", "ab", "ax", "xb", "a|b"}},
		{":regex(a*)", // matches the empty string; an empty value still never matches
			[]string{"a", "aaa"},
			[]string{"", "b", "ab"}},
		{":regex((|x))", // an empty alternative
			[]string{"x"},
			[]string{"", "y"}},
		{":regex(.+)", // "." does not cross a line break
			[]string{"a", "a b", "ż", "/"},
			[]string{"", "a\nb", "\n"}},
		{":regex((?m)^a$)", // multi-line mode stays inside; the whole value must still match
			[]string{"a"},
			[]string{"", "a\n", "\na", "a\na", "b\na"}},
		{":regex((?s).+)", // unless the expression says so
			[]string{"a\nb"},
			[]string{""}},
		{":regex(^^a$$)", // the user's own anchors are harmless
			[]string{"a"},
			[]string{"", "aa"}},
		{`:regex(\d{4}-\d{2})`,
			[]string{"2026-10"},
			[]string{"", "2026-1", "٢٠٢٦-١٠"}},
		{`:regex([^/]+/[^/]+)`, // a value may hold a decoded slash
			[]string{"a/b"},
			[]string{"", "ab", "a/b/c"}},
		{":regex(é+)",
			[]string{"é", "ééé"},
			[]string{"", "e", "é"}}, // decomposed: a different string, and values are never normalized
	}
	for _, tt := range tests {
		typ := typeOf(t, tt.suffix)
		for _, v := range tt.accept {
			if !typ.Accepts(v) {
				t.Errorf("{v%s} rejected %q", tt.suffix, v)
			}
		}
		for _, v := range tt.reject {
			if typ.Accepts(v) {
				t.Errorf("{v%s} accepted %q", tt.suffix, v)
			}
		}
	}
}

// No type accepts the empty value (PRD 5.3).
func TestNoTypeAcceptsEmpty(t *testing.T) {
	for _, suffix := range []string{"", ":int", ":number", ":regex(.*)", ":regex(a?)", ":regex((|a))", ":regex(^$|x)"} {
		if typeOf(t, suffix).Accepts("") {
			t.Errorf("{v%s} accepted the empty value", suffix)
		}
	}
}

// NFR-8: expressions that take exponential time in a backtracking engine
// take linear time here, because Go's regexp is an RE2 implementation. The
// bound is deliberately loose; a backtracking engine would not finish at
// all.
func TestRegexIsLinearTime(t *testing.T) {
	for _, source := range []string{"(a+)+b", "(a|aa)+b", "(a*)*b", "([a-z]+)*[0-9]", "(a|a)*b", "(.*)*x"} {
		typ := typeOf(t, ":regex("+source+")")
		value := strings.Repeat("a", 100_000)
		start := time.Now()
		if typ.Accepts(value) {
			t.Errorf("regex(%s) accepted a run of a's", source)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("regex(%s) took %v on 100,000 characters", source, took)
		}
	}
}

// An expression is compiled when the pattern is parsed and never again: the
// type a compiled link holds is the same value for every request.
func TestRegexIsCompiledOnce(t *testing.T) {
	c := Compile(Link{Name: "jira", Routes: []Route{
		{ID: 1, Pattern: "jira/{key:regex([A-Z]+-[0-9]+)}", Destination: "https://example.com/?k={key}"},
	}})
	typ := c.routes[0].pattern.segments[1].Type.(regexType)
	segments := []string{"jira", "OPS-1"}
	for range 3 {
		if _, ok, _, _ := c.Match(segments); !ok {
			t.Fatal("the request did not match")
		}
	}
	if again := c.routes[0].pattern.segments[1].Type.(regexType); again.re != typ.re {
		t.Error("the compiled expression was replaced between requests")
	}
	// Matching a regex route must not compile anything: a compile allocates
	// hundreds of times, a match with a small destination a handful.
	if allocs := testing.AllocsPerRun(200, func() { c.Match(segments) }); allocs > 12 {
		t.Errorf("Match on a regex route allocated %v times per call; is it compiling per request?", allocs)
	}
}

// A regular expression is not run on a value longer than
// MaxRegexValueLength; such a value is simply not accepted. Together with
// MaxRegexProgram that bounds what one check can cost.
func TestRegexValueLength(t *testing.T) {
	typ := typeOf(t, ":regex(a+)")
	if !typ.Accepts(strings.Repeat("a", MaxRegexValueLength)) {
		t.Errorf("a value of %d bytes was refused", MaxRegexValueLength)
	}
	if typ.Accepts(strings.Repeat("a", MaxRegexValueLength+1)) {
		t.Errorf("a value of %d bytes was accepted", MaxRegexValueLength+1)
	}
	// The other types have nothing to run and take a value of any length.
	long := strings.Repeat("7", 1<<20)
	for _, suffix := range []string{"", ":int", ":number"} {
		if !typeOf(t, suffix).Accepts(long) {
			t.Errorf("{v%s} refused a long value", suffix)
		}
	}
	// The worst expression that can be saved, on the longest value that is
	// checked, in every variable of a route: still far from a stall.
	source := ".{0,950}x"
	pattern := "x"
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		pattern += "/{" + name + ":regex(" + source + ")}"
	}
	c := Compile(link("x", pattern, "https://example.com/"))
	if !c.routes[0].valid {
		t.Fatal("the route did not compile")
	}
	request := []string{"x"}
	for range 6 {
		request = append(request, strings.Repeat("a", MaxRegexValueLength))
	}
	start := time.Now()
	if _, ok, _, _ := c.Match(request); ok {
		t.Error("the request matched")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("one request took %v", took)
	}
}
