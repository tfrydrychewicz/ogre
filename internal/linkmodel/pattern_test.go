package linkmodel

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// mustParse parses a pattern that the test knows to be valid.
func mustParse(t testing.TB, pattern string) Pattern {
	t.Helper()
	p, problems := ParsePattern(pattern)
	if len(problems) != 0 {
		t.Fatalf("ParsePattern(%q) problems = %+v, want none", pattern, problems)
	}
	return p
}

// describe writes a pattern's segments in a form a table can hold:
// "lit(gh) var(repo:string) var(num:int)".
func describe(p Pattern) string {
	var parts []string
	for _, seg := range p.Segments() {
		if seg.IsVariable() {
			parts = append(parts, "var("+seg.Variable+":"+seg.Type.Name()+")")
		} else {
			parts = append(parts, "lit("+seg.Literal+")")
		}
	}
	return strings.Join(parts, " ")
}

func TestParsePatternLiterals(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string // canonical text
	}{
		{"one segment", "roadmap", "roadmap"},
		{"single character", "a", "a"},
		{"single digit", "7", "7"},
		{"begins with a digit", "2fa", "2fa"},
		{"two segments", "gh/new", "gh/new"},

		// Saved in lowercase.
		{"upper case is folded", "Roadmap", "roadmap"},
		{"every segment is folded", "GH/New/PR", "gh/new/pr"},

		// Q5: separators are kept and significant.
		{"hyphen", "on-call", "on-call"},
		{"underscore", "on_call", "on_call"},
		{"no separator", "oncall", "oncall"},
		{"dot", "v1.2", "v1.2"},
		{"mixed case with a hyphen", "On-Call", "on-call"},
		{"separators at the end", "a-/b_/c.", "a-/b_/c."},
		{"doubled separators", "a--b/c__d/e..f", "a--b/c__d/e..f"},

		// Limits, at the boundary.
		{"eight segments", "a/b/c/d/e/f/g/h", "a/b/c/d/e/f/g/h"},
		{"200 characters", strings.Repeat("a", 200), strings.Repeat("a", 200)},

		// Close to reserved, but not reserved.
		{"favicon without extension", "favicon", "favicon"},
		{"reserved name in a later segment", "docs/robots.txt", "docs/robots.txt"},
		{"apple-touch-icon without .png", "apple-touch-icon", "apple-touch-icon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustParse(t, tt.pattern)
			if p.String() != tt.want || p.Shape() != tt.want {
				t.Errorf("ParsePattern(%q): canonical %q, shape %q; want both %q", tt.pattern, p.String(), p.Shape(), tt.want)
			}
			if len(p.Variables()) != 0 {
				t.Errorf("ParsePattern(%q) has variables %q", tt.pattern, p.Variables())
			}
			if p.Name() != strings.Split(tt.want, "/")[0] {
				t.Errorf("ParsePattern(%q).Name() = %q", tt.pattern, p.Name())
			}
		})
	}
}

// Decision Q5 at the parser level: the three spellings are three different
// names.
func TestParsePatternSeparatorsAreSignificant(t *testing.T) {
	names := map[string]bool{}
	for _, p := range []string{"on-call", "on_call", "oncall"} {
		names[mustParse(t, p).Name()] = true
	}
	if len(names) != 3 {
		t.Errorf("got %d distinct names %v, want 3", len(names), names)
	}
}

func TestParsePatternVariables(t *testing.T) {
	sixVars := "x/{a}/{b}/{c}/{d}/{e}/{f}"
	// The longest expression a pattern has room for: 200 characters in all,
	// counted in characters (é is two bytes), 13 of them around the
	// expression. MaxRegexLength itself is out of reach while a whole
	// pattern is held to the same 200.
	long := "x/{v:regex(" + strings.Repeat("é", 187) + ")}"
	tests := []struct {
		name      string
		pattern   string
		canonical string // "" means: the same as pattern
		shape     string
		segments  string
	}{
		{"text variable", "gh/{repo}", "", "gh/{}", "lit(gh) var(repo:string)"},
		{"explicit string is the default", "gh/{repo:string}", "gh/{repo}", "gh/{}", "lit(gh) var(repo:string)"},
		{"int", "gh/{id:int}", "", "gh/{:int}", "lit(gh) var(id:int)"},
		{"number", "rate/{x:number}", "", "rate/{:number}", "lit(rate) var(x:number)"},
		{"regex", "jira/{key:regex([A-Z]+-[0-9]+)}", "", "jira/{:regex([A-Z]+-[0-9]+)}", "lit(jira) var(key:regex)"},
		{"PRD 5.1", "gh/{repo}/pr/{num:int}", "", "gh/{}/pr/{:int}", "lit(gh) var(repo:string) lit(pr) var(num:int)"},
		{"PRD 5.7 month", "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "",
			"reports/{:regex(^(0?[1-9]|1[0-2])$)}/{:int}", "lit(reports) var(month:regex) var(day:int)"},

		// Literals fold; variable names and expressions are kept as written.
		{"case", "GH/{Repo}/PR/{Num:int}", "gh/{Repo}/pr/{Num:int}", "gh/{}/pr/{:int}",
			"lit(gh) var(Repo:string) lit(pr) var(Num:int)"},
		{"type keyword in any case", "x/{a:INT}/{b:Number}/{c:String}/{d:REGEX([A-Z])}", "x/{a:int}/{b:number}/{c}/{d:regex([A-Z])}",
			"x/{:int}/{:number}/{}/{:regex([A-Z])}", "lit(x) var(a:int) var(b:number) var(c:string) var(d:regex)"},
		{"name with digits and underscore", "x/{a_1}/{B2_}", "", "x/{}/{}", "lit(x) var(a_1:string) var(B2_:string)"},

		// What an expression may contain without ending the variable or the
		// segment (PRD 5.2).
		{"slash in a regex", "d/{x:regex(a/b)}/e", "", "d/{:regex(a/b)}/e", "lit(d) var(x:regex) lit(e)"},
		{"braces in a regex", "d/{x:regex(a{2,3})}", "", "d/{:regex(a{2,3})}", "lit(d) var(x:regex)"},
		{"closing brace in a class", "d/{x:regex([{}]+)}", "", "d/{:regex([{}]+)}", "lit(d) var(x:regex)"},
		{"parenthesis in a class", "d/{x:regex([()]+)}", "", "d/{:regex([()]+)}", "lit(d) var(x:regex)"},
		{"closing bracket first in a class", "d/{x:regex([]a)]+)}", "", "d/{:regex([]a)]+)}", "lit(d) var(x:regex)"},
		{"negated class with a leading bracket", "d/{x:regex([^])]+)}", "", "d/{:regex([^])]+)}", "lit(d) var(x:regex)"},
		{"named class", "d/{x:regex([[:alpha:])]+)}", "", "d/{:regex([[:alpha:])]+)}", "lit(d) var(x:regex)"},
		{"escaped parentheses", `d/{x:regex(\(a\))}`, "", `d/{:regex(\(a\))}`, "lit(d) var(x:regex)"},
		{"escaped bracket then parenthesis", `d/{x:regex(\[(a)\])}`, "", `d/{:regex(\[(a)\])}`, "lit(d) var(x:regex)"},
		{"escaped backslash before the end", `d/{x:regex(a\\)}`, "", `d/{:regex(a\\)}`, "lit(d) var(x:regex)"},
		{"quoted parenthesis", `d/{x:regex(\Q(\E+)}`, "", `d/{:regex(\Q(\E+)}`, "lit(d) var(x:regex)"},
		{"nested groups", "d/{x:regex(((a|b)(c(d)))+)}", "", "d/{:regex(((a|b)(c(d)))+)}", "lit(d) var(x:regex)"},
		{"flags", "d/{x:regex((?i)abc)}", "", "d/{:regex((?i)abc)}", "lit(d) var(x:regex)"},
		{"non-ASCII in a regex", "d/{x:regex(ż+)}", "", "d/{:regex(ż+)}", "lit(d) var(x:regex)"},
		{"everything at once", `d/{x:regex(^(a/b|[)}/]|\)|c{1})$)}/e/{y}`, "", `d/{:regex(^(a/b|[)}/]|\)|c{1})$)}/e/{}`,
			"lit(d) var(x:regex) lit(e) var(y:string)"},
		{"a regex that matches the empty string", "d/{x:regex(a*)}", "", "d/{:regex(a*)}", "lit(d) var(x:regex)"},

		// Limits, at the boundary.
		{"six variables", sixVars, "", "x/{}/{}/{}/{}/{}/{}",
			"lit(x) var(a:string) var(b:string) var(c:string) var(d:string) var(e:string) var(f:string)"},
		{"200 characters, most of them an expression", long, "", "x/{:regex(" + strings.Repeat("é", 187) + ")}", "lit(x) var(v:regex)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mustParse(t, tt.pattern)
			canonical := tt.canonical
			if canonical == "" {
				canonical = tt.pattern
			}
			if p.String() != canonical {
				t.Errorf("String() = %q, want %q", p.String(), canonical)
			}
			if p.Shape() != tt.shape {
				t.Errorf("Shape() = %q, want %q", p.Shape(), tt.shape)
			}
			if got := describe(p); got != tt.segments {
				t.Errorf("segments = %s, want %s", got, tt.segments)
			}
			if Shape(tt.pattern) != tt.shape || Shape(canonical) != tt.shape {
				t.Errorf("Shape(%q) = %q and of its canonical text %q, want %q",
					tt.pattern, Shape(tt.pattern), Shape(canonical), tt.shape)
			}
			checkRoundTrip(t, tt.pattern, p)
		})
	}
}

// checkRoundTrip asserts what holds for every valid pattern: its canonical
// text parses to an equal pattern, segments carry offsets into the text
// that was parsed, and the variables are the variable segments in order.
func checkRoundTrip(t testing.TB, text string, p Pattern) {
	t.Helper()
	again, problems := ParsePattern(p.String())
	if len(problems) != 0 {
		t.Fatalf("canonical text %q of %q does not parse: %+v", p.String(), text, problems)
	}
	if !samePattern(p, again) {
		t.Fatalf("canonical text %q of %q parses to a different pattern: %q, shape %q",
			p.String(), text, again.String(), again.Shape())
	}
	if utf8.RuneCountInString(p.String()) > utf8.RuneCountInString(text) {
		t.Errorf("canonical text %q is longer than %q; it could then exceed a limit the text met", p.String(), text)
	}

	var variables []string
	previous := -1
	for i, seg := range p.Segments() {
		if seg.Start <= previous || seg.End <= seg.Start || seg.End > len(text) {
			t.Fatalf("segment %d of %q has range [%d,%d)", i, text, seg.Start, seg.End)
		}
		previous = seg.End
		source := text[seg.Start:seg.End]
		switch {
		case seg.IsVariable():
			variables = append(variables, seg.Variable)
			if seg.Literal != "" || !strings.HasPrefix(source, "{"+seg.Variable) || !strings.HasSuffix(source, "}") {
				t.Errorf("variable segment %d of %q = %+v over %q", i, text, seg, source)
			}
		case seg.Variable != "" || seg.Literal != NormalizeName(source) || seg.Literal == "":
			t.Errorf("literal segment %d of %q = %+v over %q", i, text, seg, source)
		}
	}
	if !slices.Equal(variables, p.Variables()) {
		t.Errorf("Variables() of %q = %q, want %q", text, p.Variables(), variables)
	}
	if len(p.Segments()) == 0 || p.Segments()[0].IsVariable() || p.Name() != p.Segments()[0].Literal {
		t.Errorf("pattern %q does not begin with its name: %q", text, p.Name())
	}
}

// samePattern reports whether two patterns are equal in everything but the
// offsets of their segments.
func samePattern(a, b Pattern) bool {
	if a.String() != b.String() || a.Shape() != b.Shape() || len(a.Segments()) != len(b.Segments()) ||
		!slices.Equal(a.Variables(), b.Variables()) {
		return false
	}
	for i, x := range a.Segments() {
		y := b.Segments()[i]
		if x.Literal != y.Literal || x.Variable != y.Variable || x.IsVariable() != y.IsVariable() {
			return false
		}
		if x.IsVariable() && (x.Type.Name() != y.Type.Name() || x.Type.Format() != y.Type.Format()) {
			return false
		}
	}
	return true
}

func TestParsePatternProblems(t *testing.T) {
	type want struct {
		code       string
		start, end int
	}
	long := strings.Repeat("a", 201)
	bigRegex := "x/{v:regex((?:" + strings.Repeat("a", 120) + "){1000})}"
	tests := []struct {
		name    string
		pattern string
		want    []want
	}{
		{"empty", "", []want{{CodePatternEmpty, 0, 0}}},

		// Empty segments are an error in a pattern (a request drops them).
		{"double slash", "a//b", []want{{CodePatternEmptySegment, 2, 3}}},
		{"leading slash", "/a", []want{{CodePatternEmptySegment, 0, 1}}},
		{"trailing slash", "a/", []want{{CodePatternEmptySegment, 1, 2}}},
		{"only a slash", "/", []want{{CodePatternEmptySegment, 0, 1}}},
		{"two empty segments", "a///b", []want{
			{CodePatternEmptySegment, 2, 3}, {CodePatternEmptySegment, 3, 4}}},
		{"trailing slash after a variable", "a/{b}/", []want{{CodePatternEmptySegment, 5, 6}}},

		// A literal begins with a letter or digit.
		{"leading hyphen", "-a", []want{{CodePatternBadStart, 0, 1}}},
		{"leading dot", ".a", []want{{CodePatternBadStart, 0, 1}}},
		{"leading underscore", "_a", []want{{CodePatternBadStart, 0, 1}}},
		{"dot segment", "a/./b", []want{{CodePatternBadStart, 2, 3}}},
		{"dot dot segment", "a/../b", []want{{CodePatternBadStart, 2, 3}}},
		{"leading hyphen in a later segment", "gh/-new", []want{{CodePatternBadStart, 3, 4}}},

		// Characters outside the literal alphabet.
		{"space", "a b", []want{{CodePatternBadCharacter, 1, 2}}},
		{"leading space", " a", []want{{CodePatternBadCharacter, 0, 1}}},
		{"percent escape", "a%20b", []want{{CodePatternBadCharacter, 1, 2}}},
		{"question mark", "gh?x=1", []want{{CodePatternBadCharacter, 2, 3}}},
		{"colon from a pasted URL", "http://x", []want{
			{CodePatternBadCharacter, 4, 5}, {CodePatternEmptySegment, 6, 7}}},
		{"one problem per segment", "a!b!c/d?e", []want{
			{CodePatternBadCharacter, 1, 2}, {CodePatternBadCharacter, 7, 8}}},
		{"non-ASCII letter", "café", []want{{CodePatternBadCharacter, 3, 5}}},
		{"non-ASCII first letter", "über", []want{{CodePatternBadCharacter, 0, 2}}},
		{"non-ASCII in a later segment", "gh/日本", []want{{CodePatternBadCharacter, 3, 6}}},
		{"kelvin sign", "\u212a", []want{{CodePatternBadCharacter, 0, 3}}},
		{"invalid UTF-8", "a\xffb", []want{{CodePatternBadCharacter, 1, 2}}},
		{"replacement character", "a�b", []want{{CodePatternBadCharacter, 1, 4}}},
		{"NUL", "a\x00b", []want{{CodePatternBadCharacter, 1, 2}}},
		{"newline", "a\nb", []want{{CodePatternBadCharacter, 1, 2}}},

		// Braces that do not make a variable.
		{"unclosed variable", "gh/{repo", []want{{CodePatternUnclosedVariable, 3, 8}}},
		{"unclosed, more segments follow", "gh/{repo/pr/{num}", []want{{CodePatternUnclosedVariable, 3, 8}}},
		{"unclosed typed variable", "gh/{num:int", []want{{CodePatternUnclosedVariable, 3, 11}}},
		{"unclosed before the next variable", "gh/{a:int{b}", []want{{CodePatternUnclosedVariable, 3, 9}}},
		{"only an opening brace", "gh/{", []want{{CodePatternUnclosedVariable, 3, 4}}},
		{"plan 3.5 example", "reports/{month:int}/{day", []want{{CodePatternUnclosedVariable, 20, 24}}},
		{"closing brace alone", "a}", []want{{CodePatternUnmatchedBrace, 1, 2}}},
		{"closing brace in a later segment", "gh/repo}", []want{{CodePatternUnmatchedBrace, 7, 8}}},
		{"one closing brace too many", "gh/{n:int}}", []want{{CodePatternUnmatchedBrace, 10, 11}}},
		{"brace inside a literal", "a{b", []want{{CodePatternUnclosedVariable, 1, 3}}},

		// Variable names.
		{"empty name", "gh/{}", []want{{CodePatternEmptyVariableName, 3, 5}}},
		{"empty name with a type", "gh/{:int}", []want{{CodePatternEmptyVariableName, 3, 9}}},
		{"name begins with a digit", "gh/{1a}", []want{{CodePatternBadVariableName, 4, 6}}},
		{"name begins with an underscore", "gh/{_a}", []want{{CodePatternBadVariableName, 4, 6}}},
		{"hyphen in a name", "gh/{pull-id}", []want{{CodePatternBadVariableName, 4, 11}}},
		{"space in a name", "gh/{ repo }", []want{{CodePatternBadVariableName, 4, 10}}},
		{"dot in a name", "gh/{a.b}", []want{{CodePatternBadVariableName, 4, 7}}},
		{"non-ASCII name", "gh/{naïve}", []want{{CodePatternBadVariableName, 4, 10}}},
		{"plus prefix of a later milestone", "gh/{+rest}", []want{{CodePatternBadVariableName, 4, 9}}},
		{"duplicate name", "gh/{a}/{a}", []want{{CodePatternDuplicateVariable, 8, 9}}},
		{"duplicate name, different types", "gh/{id}/x/{id:int}", []want{{CodePatternDuplicateVariable, 11, 13}}},
		{"duplicate ignoring case", "gh/{Repo}/{repo}", []want{{CodePatternDuplicateVariable, 11, 15}}},
		{"used three times", "gh/{a}/{a}/{a}", []want{
			{CodePatternDuplicateVariable, 8, 9}, {CodePatternDuplicateVariable, 12, 13}}},

		// Where a variable may stand.
		{"variable first", "{name}", []want{{CodePatternVariableFirst, 0, 6}}},
		{"variable first, more follows", "{team}/docs", []want{{CodePatternVariableFirst, 0, 6}}},
		{"typed variable first", "{id:int}/x", []want{{CodePatternVariableFirst, 0, 8}}},
		{"unclosed variable first", "{name", []want{
			{CodePatternUnclosedVariable, 0, 5}, {CodePatternVariableFirst, 0, 5}}},
		{"text then a variable", "gh/v{n}", []want{{CodePatternMixedSegment, 3, 7}}},
		{"variable then text", "gh/{n}.html", []want{{CodePatternMixedSegment, 3, 11}}},
		{"two variables", "gh/{a}{b}", []want{{CodePatternMixedSegment, 3, 9}}},
		{"text around a variable", "gh/a{b}c", []want{{CodePatternMixedSegment, 3, 8}}},
		{"mixed first segment", "v{n}", []want{{CodePatternMixedSegment, 0, 4}}},
		{"mixed, with a regex holding a slash", "gh/v{n:regex(a/b)}/x", []want{{CodePatternMixedSegment, 3, 18}}},

		// Types.
		{"unknown type", "gh/{n:date}", []want{{CodePatternUnknownType, 6, 10}}},
		{"uuid is a later milestone", "gh/{n:uuid}", []want{{CodePatternUnknownType, 6, 10}}},
		{"path is a later milestone", "gh/{rest:path}", []want{{CodePatternUnknownType, 9, 13}}},
		{"int range is a later milestone", "gh/{n:int(1..12)}", []want{{CodePatternUnknownType, 6, 16}}},
		{"enum is a later milestone", "gh/{e:enum(prod,dev)}", []want{{CodePatternUnknownType, 6, 20}}},
		{"integer spelled out", "gh/{n:integer}", []want{{CodePatternUnknownType, 6, 13}}},
		{"empty type", "gh/{n:}", []want{{CodePatternUnknownType, 5, 6}}},
		{"regex without parentheses", "gh/{n:regex}", []want{{CodePatternUnknownType, 6, 11}}},
		{"two colons", "gh/{n:int:x}", []want{{CodePatternUnknownType, 6, 11}}},
		{"bad name and unknown type", "gh/{1:date}", []want{
			{CodePatternBadVariableName, 4, 5}, {CodePatternUnknownType, 6, 10}}},

		// Regular expressions.
		{"missing closing parenthesis", "gh/{n:regex((a)}", []want{{CodePatternRegexUnbalanced, 6, 16}}},
		{"nothing closes regex(", "gh/{n:regex(abc", []want{{CodePatternRegexUnbalanced, 6, 15}}},
		{"unbalanced swallows the rest", "gh/{n:regex([0-9]+}/x/{y}", []want{{CodePatternRegexUnbalanced, 6, 25}}},
		{"class never closed", "gh/{n:regex([a-z)}", []want{{CodePatternRegexUnbalanced, 6, 18}}},
		{"escape hides the parenthesis", `gh/{n:regex(a\)}`, []want{{CodePatternRegexUnbalanced, 6, 16}}},
		{"quote never closed", `gh/{n:regex(\Qa)}`, []want{{CodePatternRegexUnbalanced, 6, 17}}},
		{"text after the expression", "gh/{n:regex(a)b}", []want{{CodePatternRegexTrailingText, 14, 15}}},
		{"one parenthesis too many", "gh/{n:regex(a))}", []want{{CodePatternRegexTrailingText, 14, 15}}},
		{"expression closed, variable not", "gh/{n:regex(a)", []want{{CodePatternUnclosedVariable, 3, 14}}},
		{"expression closed, variable not, more follows", "gh/{n:regex(a)/x", []want{{CodePatternUnclosedVariable, 3, 14}}},
		{"empty expression", "gh/{n:regex()}", []want{{CodePatternRegexEmpty, 6, 13}}},
		{"backreference", `gh/{n:regex((a)\1)}`, []want{{CodePatternRegexInvalid, 12, 17}}},
		{"lookahead", "gh/{n:regex(a(?=b))}", []want{{CodePatternRegexInvalid, 12, 18}}},
		{"negative lookbehind", "gh/{n:regex((?<!a)b)}", []want{{CodePatternRegexInvalid, 12, 19}}},
		{"nothing to repeat", "gh/{n:regex(*a)}", []want{{CodePatternRegexInvalid, 12, 14}}},
		{"repeat of a repeat", "gh/{n:regex(a**)}", []want{{CodePatternRegexInvalid, 12, 15}}},
		{"bad class range", "gh/{n:regex([z-a])}", []want{{CodePatternRegexInvalid, 12, 17}}},
		{"unknown named class", "gh/{n:regex([[:foo:]])}", []want{{CodePatternRegexInvalid, 12, 21}}},
		{"repeat count over 1000", "gh/{n:regex(a{1001})}", []want{{CodePatternRegexInvalid, 12, 19}}},
		{"nested counts over 1000", "gh/{n:regex((a{100}){11})}", []want{{CodePatternRegexInvalid, 12, 24}}},
		{"bad escape", `gh/{n:regex(\y)}`, []want{{CodePatternRegexInvalid, 12, 14}}},
		{"invalid UTF-8 in an expression", "gh/{n:regex(a\xffb)}", []want{{CodePatternRegexInvalid, 12, 15}}},
		{"expression of 201 characters", "x/{v:regex(" + strings.Repeat("a", 201) + ")}", []want{
			{CodePatternRegexTooLong, 11, 212}, {CodePatternTooLong, 200, 214}}},
		{"expression compiles too large", bigRegex, []want{{CodePatternRegexTooComplex, 11, 141}}},

		// Limits.
		{"201 characters", long, []want{{CodePatternTooLong, 200, 201}}},
		{"too long, counted in characters", strings.Repeat("a", 199) + "éé", []want{
			{CodePatternBadCharacter, 199, 201}, {CodePatternTooLong, 201, 203}}},
		{"too long and a variable", long + "/{x}", []want{{CodePatternTooLong, 200, 205}}},
		{"too long with an unclosed variable before the limit", strings.Repeat("a", 100) + "{" + strings.Repeat("a", 100), []want{
			{CodePatternUnclosedVariable, 100, 201}, {CodePatternTooLong, 200, 201}}},
		{"nine segments", "a/b/c/d/e/f/g/h/i", []want{{CodePatternTooManySegments, 16, 17}}},
		{"eleven segments", "a/b/c/d/e/f/g/h/i/j/k", []want{{CodePatternTooManySegments, 16, 21}}},
		{"eight segments and a trailing slash", "a/b/c/d/e/f/g/h/", []want{{CodePatternTooManySegments, 16, 16}}},
		{"nine segments with variables", "a/{b}/{c}/{d}/e/f/g/h/{i}", []want{{CodePatternTooManySegments, 22, 25}}},
		{"seven variables", "x/{a}/{b}/{c}/{d}/{e}/{f}/{g}", []want{{CodePatternTooManyVariables, 26, 29}}},

		// Reserved names (plan 3.3), ignoring case, first segment only.
		{"favicon.ico", "favicon.ico", []want{{CodePatternReservedName, 0, 11}}},
		{"favicon.ico in upper case", "FAVICON.ICO", []want{{CodePatternReservedName, 0, 11}}},
		{"robots.txt", "robots.txt", []want{{CodePatternReservedName, 0, 10}}},
		{"sitemap.xml", "sitemap.xml", []want{{CodePatternReservedName, 0, 11}}},
		{"apple-touch-icon.png", "apple-touch-icon.png", []want{{CodePatternReservedName, 0, 20}}},
		{"apple-touch-icon variant", "apple-touch-icon-120x120.png", []want{{CodePatternReservedName, 0, 28}}},
		{"reserved with a sub-path", "robots.txt/x", []want{{CodePatternReservedName, 0, 10}}},
		{"reserved with a variable", "robots.txt/{x}", []want{{CodePatternReservedName, 0, 10}}},
		{".well-known", ".well-known/x", []want{
			{CodePatternBadStart, 0, 1}, {CodePatternReservedName, 0, 11}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, problems := ParsePattern(tt.pattern)
			if len(p.Segments()) != 0 || p.String() != "" || p.Shape() != "" || p.Name() != "" || p.Variables() != nil {
				t.Errorf("ParsePattern(%q) returned a pattern %+v for an invalid text", tt.pattern, p)
			}
			var got []want
			for _, p := range problems {
				got = append(got, want{p.Code, p.Start, p.End})
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ParsePattern(%q) problems =\n  %+v\nwant\n  %+v", tt.pattern, problems, tt.want)
			}
			checkProblems(t, tt.pattern, FieldPattern, problems)
			// A store asks for the shape of whatever text it holds; text
			// that is not a pattern comes back unchanged. (The size of a
			// compiled expression is a rule for saving only; see
			// TestStoredPatternIsNotHeldToProgramSize.)
			if got := Shape(tt.pattern); got != tt.pattern && tt.pattern != bigRegex {
				t.Errorf("Shape(%q) = %q, want the text unchanged", tt.pattern, got)
			}
		})
	}
}

// Messages are the only help a user gets, so the ones for the mistakes people
// actually make are pinned down: they must say what to do, in words.
func TestParsePatternMessages(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string // every one must appear in the first problem's message
	}{
		{"gh/{repo", []string{"missing its closing }"}},
		{"gh/v{n}", []string{"either fixed text or one variable", `"v/{n}"`}},
		{"{team}/docs", []string{"begin with the link's name", "can't begin with a variable"}},
		{"gh/{n:date}", []string{`"date" is not a known type`, "string, int, number and regex(…)"}},
		{"gh/{n:int(1..12)}", []string{`"int(1..12)" is not a known type`}},
		{"gh/{n:regex}", []string{"in parentheses", "regex([0-9]+)"}},
		{"gh/{1a}", []string{`"1a" is not a variable name`, "begins with a letter"}},
		{"gh/{a}/{A}", []string{"already has a variable named A", "upper and lower case"}},
		{`gh/{n:regex((a)\1)}`, []string{"not valid", `backreferences such as \1 are not supported`}},
		{"gh/{n:regex(a(?=b))}", []string{"lookahead and lookbehind", "not supported"}},
		{"gh/{n:regex((?<=a)b)}", []string{"lookahead and lookbehind", "not supported"}},
		{"gh/{n:regex(*a)}", []string{`"*" has nothing before it to repeat`}},
		{"gh/{n:regex(a{1001})}", []string{`"{1001}" is too large`}},
		{"gh/{n:regex([z-a])}", []string{`"z-a" is not a valid range`}},
		{"gh/{n:regex((a)}", []string{"The parenthesis that closes regex( is missing"}},
		{"x/{v:regex((?:" + strings.Repeat("a", 120) + "){1000})}", []string{"too complex", "repeat count"}},
	}
	for _, tt := range tests {
		_, problems := ParsePattern(tt.pattern)
		if len(problems) == 0 {
			t.Errorf("ParsePattern(%q) reported no problem", tt.pattern)
			continue
		}
		for _, want := range tt.want {
			if !strings.Contains(problems[0].Message, want) {
				t.Errorf("ParsePattern(%q) message = %q, want it to contain %q", tt.pattern, problems[0].Message, want)
			}
		}
		// What the engine calls it is of no use to the person typing.
		for _, jargon := range []string{"Perl", "syntax", "error parsing", "regexp:", "`"} {
			if strings.Contains(problems[0].Message, jargon) {
				t.Errorf("ParsePattern(%q) message %q repeats the engine's wording (%q)", tt.pattern, problems[0].Message, jargon)
			}
		}
	}
}

// checkProblems asserts what holds for every problem regardless of its code:
// it names its field, has a readable message, and its offsets are ordered
// byte offsets inside the input that fall between characters.
func checkProblems(t testing.TB, input, field string, problems []Problem) {
	t.Helper()
	prev := 0
	for _, p := range problems {
		if p.Severity != SeverityError && p.Severity != SeverityWarning {
			t.Errorf("problem %s: severity = %q", p.Code, p.Severity)
		}
		if p.Field != field {
			t.Errorf("problem %s: field = %q, want %q", p.Code, p.Field, field)
		}
		if !strings.HasPrefix(p.Code, field+".") {
			t.Errorf("problem code %q does not start with %q", p.Code, field+".")
		}
		if p.Message == "" || !utf8.ValidString(p.Message) || !strings.HasSuffix(p.Message, ".") {
			t.Errorf("problem %s: message %q is not a plain sentence", p.Code, p.Message)
		}
		if strings.ContainsAny(p.Message, "\r\n\t\x00") {
			t.Errorf("problem %s: message %q contains a control character", p.Code, p.Message)
		}
		if p.Start < 0 || p.Start > p.End || p.End > len(input) {
			t.Errorf("problem %s: range [%d,%d) is outside the input of %d bytes", p.Code, p.Start, p.End, len(input))
		}
		if p.Start < prev {
			t.Errorf("problem %s: start %d comes before the previous problem's %d", p.Code, p.Start, prev)
		}
		prev = p.Start
	}
}

func TestPatternTokens(t *testing.T) {
	lit := func(start, end int) Token { return Token{Kind: TokenLiteral, Start: start, End: end} }
	v := func(start, end int, name, typ string) Token {
		return Token{Kind: TokenVariable, Start: start, End: end, Name: name, Type: typ}
	}
	tests := []struct {
		pattern string
		want    []Token
	}{
		{"", nil},
		{"gh", []Token{lit(0, 2)}},
		{"gh/{repo}/pr/{num:int}", []Token{lit(0, 2), v(3, 9, "repo", "string"), lit(10, 12), v(13, 22, "num", "int")}},
		{"jira/{key:regex([A-Z]+-[0-9]+)}", []Token{lit(0, 4), v(5, 31, "key", "regex")}},
		{"x/{n:number}/{s:string}", []Token{lit(0, 1), v(2, 12, "n", "number"), v(13, 23, "s", "string")}},
		// Plan 3.5: what could be read is a token, the rest is a problem.
		{"reports/{month:int}/{day", []Token{lit(0, 7), v(8, 19, "month", "int")}},
		{"gh/v{n}/x", []Token{lit(0, 2), lit(8, 9)}},
		{"gh//x", []Token{lit(0, 2), lit(4, 5)}},
		{"/", nil},
		{"gh/{n:date}/{m}", []Token{lit(0, 2), v(12, 15, "m", "string")}},
		{"a b/c", []Token{lit(4, 5)}},
		// A duplicate name is well-formed where it stands; the problem says
		// the rest.
		{"gh/{a}/{a}", []Token{lit(0, 2), v(3, 6, "a", "string"), v(7, 10, "a", "string")}},
		{"{x}/a", []Token{v(0, 3, "x", "string"), lit(4, 5)}},
	}
	for _, tt := range tests {
		if got := PatternTokens(tt.pattern); !slices.Equal(got, tt.want) {
			t.Errorf("PatternTokens(%q) =\n  %+v\nwant\n  %+v", tt.pattern, got, tt.want)
		}
	}
}

// The tokens of a valid pattern are exactly its segments.
func TestPatternTokensMatchSegments(t *testing.T) {
	for _, text := range []string{"gh", "GH/{Repo}/pr/{num:INT}", "d/{x:regex(a/b)}/e/{y:number}"} {
		p := mustParse(t, text)
		tokens := PatternTokens(text)
		if len(tokens) != len(p.Segments()) {
			t.Fatalf("PatternTokens(%q) has %d tokens for %d segments", text, len(tokens), len(p.Segments()))
		}
		for i, seg := range p.Segments() {
			want := Token{Kind: TokenLiteral, Start: seg.Start, End: seg.End}
			if seg.IsVariable() {
				want = Token{Kind: TokenVariable, Start: seg.Start, End: seg.End, Name: seg.Variable, Type: seg.Type.Name()}
			}
			if tokens[i] != want {
				t.Errorf("PatternTokens(%q)[%d] = %+v, want %+v", text, i, tokens[i], want)
			}
		}
	}
}

func TestRegexEnd(t *testing.T) {
	tests := []struct {
		text string // what follows "regex("
		want int    // offset of the closing parenthesis, -1 for none
	}{
		{"a)", 1},
		{")", 0},
		{"(a))", 3},
		{"((a)(b)))", 8},
		{"a", -1},
		{"(a)", -1},
		{"", -1},
		{`\))`, 2},
		{`\)`, -1},
		{`\\)`, 2},
		{`a\`, -1},
		{"[)])", 3},
		{"[^)])", 4},
		{"[])])", 4},
		{"[^])])", 5},
		{`[\])])`, 5},
		{"[[:alpha:])])", 12},
		{"[[:alpha:]])", 11},
		{"[a-z", -1},
		{"[a-z)", -1},
		{`\Q)\E)`, 5},
		{`\Q)`, -1},
		{`\Qa\E`, -1},
		{"a/b{2})}", 6},
		{"é)", 2},
		{"[é)]é)", 7},
		{"(?i)a)", 5},
		{"(?P<n>a))", 8},
	}
	for _, tt := range tests {
		if got := regexEnd(tt.text, 0); got != tt.want {
			t.Errorf("regexEnd(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

// The tokenizer decides where an expression ends; Go's regexp decides
// whether it is one. Whenever the parser accepts an expression, the two must
// have agreed: the text taken is balanced the way RE2 reads it.
func TestRegexEndAgreesWithRE2(t *testing.T) {
	for _, source := range []string{
		"a", "(a)", "((a)(b))", `\(`, `\)`, `\\`, "[)]", "[(]", "[^)]", "[])]", "[^])]", `[\]]`,
		"[[:alpha:]]", "[[:alpha:])]", `\Q(\E`, `\Q)\E`, `\Q[\E`, "(?i)a", "(?P<n>a)", "(?:a|b)",
		"a{2}", "a{2,}", "{", "}", "a/b", "[/{}]", `\{`, "^(0?[1-9]|1[0-2])$", "[A-Z]+-[0-9]+", `\pL`, `\p{Greek}`,
		`[\pL)]`, `\x{10FFFF}`, `[a\]b)]`, "[a-]", "[]-a]", "(|)", "()", `\Q\E`, `a\Qb\Ec`, `[[]`, `[[)]`, `[[:)]`,
	} {
		pattern := "x/{v:regex(" + source + ")}"
		p, problems := ParsePattern(pattern)
		if len(problems) != 0 {
			t.Errorf("ParsePattern(%q) problems = %+v; the expression is valid RE2", pattern, problems)
			continue
		}
		if got := p.Segments()[1].Type.Format(); got != "regex("+source+")" {
			t.Errorf("ParsePattern(%q) took the expression %q", pattern, got)
		}
		checkRoundTrip(t, pattern, p)
	}
}

func TestZeroPattern(t *testing.T) {
	var p Pattern
	for _, segments := range [][]string{nil, {}, {""}, {"gh"}} {
		if p.fits(segments) {
			t.Errorf("the zero Pattern fits %q", segments)
		}
	}
	if p.Name() != "" || p.String() != "" || p.Shape() != "" || len(p.Segments()) != 0 || p.Variables() != nil {
		t.Errorf("the zero Pattern is not empty: %+v", p)
	}
}

// Shape is what the store's unique index is built on: equal shapes are the
// same pattern once variable names are ignored (PRD 5.5), and nothing else
// is ignored.
func TestShapeEquality(t *testing.T) {
	same := [][]string{
		{"gh/{repo}", "gh/{name}", "GH/{Repo}", "gh/{repo:string}"},
		{"gh/{id:int}", "gh/{num:INT}"},
		{"gh/{a}/pr/{b:int}", "gh/{x}/pr/{y:int}"},
		{"j/{k:regex([A-Z]+)}", "j/{key:regex([A-Z]+)}", "J/{key:REGEX([A-Z]+)}"},
	}
	different := []string{
		"gh", "gh/new", "gh/{repo}", "gh/{id:int}", "gh/{x:number}", "gh/{x:regex([0-9]+)}", "gh/{x:regex([0-9]*)}",
		"gh/{x:regex((?i)a)}", "gh/{x:regex(a)}", "gh/{x:regex(A)}", "gh/{a}/{b}", "gh/{a}/b", "gh/a/{b}", "gh/{a:int}/{b}", "gh/{a}/{b:int}",
	}
	for _, group := range same {
		for _, text := range group[1:] {
			if a, b := mustParse(t, group[0]).Shape(), mustParse(t, text).Shape(); a != b {
				t.Errorf("Shape(%q) = %q and Shape(%q) = %q; want them equal", group[0], a, text, b)
			}
		}
	}
	seen := map[string]string{}
	for _, text := range different {
		shape := mustParse(t, text).Shape()
		if other, dup := seen[shape]; dup {
			t.Errorf("%q and %q have the same shape %q", other, text, shape)
		}
		seen[shape] = text
	}
}

func TestPatternFits(t *testing.T) {
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
		{"text variable", "gh/{repo}", []string{"gh", "Api-Server"}, true},
		{"int variable", "gh/{id:int}", []string{"gh", "42"}, true},
		{"regex variable", "jira/{key:regex([A-Z]+-[0-9]+)}", []string{"JIRA", "OPS-1"}, true},

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
		{"int refuses text", "gh/{id:int}", []string{"gh", "x"}, false},
		{"regex is case-sensitive", "jira/{key:regex([A-Z]+-[0-9]+)}", []string{"jira", "ops-1"}, false},
		{"empty value", "gh/{repo}", []string{"gh", ""}, false},
		{"variable with nothing for it", "gh/{repo}", []string{"gh"}, false},

		{"no request segments", "gh", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustParse(t, tt.pattern).fits(tt.segments); got != tt.want {
				t.Errorf("pattern %q fits(%q) = %v, want %v", tt.pattern, tt.segments, got, tt.want)
			}
		})
	}
}

// MaxRegexProgram guards what is saved. A pattern that is already stored is
// read back without it: how large an expression compiles is a detail of the
// Go release, and a route must not stop matching because Go was upgraded.
func TestStoredPatternIsNotHeldToProgramSize(t *testing.T) {
	source := "(?:a?){510}" // about 1,020 instructions
	pattern := "x/{v:regex(" + source + ")}"
	if _, problems := ParsePattern(pattern); len(problems) != 1 || problems[0].Code != CodePatternRegexTooComplex {
		t.Fatalf("ParsePattern problems = %+v, want %s", problems, CodePatternRegexTooComplex)
	}
	if got, want := Shape(pattern), "x/{:regex("+source+")}"; got != want {
		t.Errorf("Shape of the stored pattern = %q, want %q", got, want)
	}
	c := Compile(Link{Name: "x", Routes: []Route{{ID: 1, Pattern: pattern, Destination: "https://example.com/?v={v}"}}})
	if _, ok, _, err := c.Match([]string{"x", strings.Repeat("a", 200)}); !ok || err != nil {
		t.Errorf("the stored route did not match: ok %v, err %v", ok, err)
	}
	// One repeat fewer is under the limit and can be saved.
	mustParse(t, "x/{v:regex((?:a?){490})}")
	// The store reads its patterns the same lenient way.
	if stored, ok := StoredPattern(pattern); !ok || !slices.Equal(stored.Variables(), []string{"v"}) {
		t.Errorf("StoredPattern = %v, %v; want the variable v", stored.Variables(), ok)
	}
	if _, ok := StoredPattern("x/{v"); ok {
		t.Error("StoredPattern read text that is not a pattern")
	}
	// Ordinary expressions are nowhere near the limit.
	for _, source := range []string{"^(0?[1-9]|1[0-2])$", "[A-Z]+-[0-9]+", "[0-9a-f]{64}", ".{1,200}",
		"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", `\d{4}-\d{2}-\d{2}`, "(?i)[a-z0-9._%+-]{1,64}@[a-z0-9.-]{1,180}"} {
		mustParse(t, "x/{v:regex("+source+")}")
	}
}

// Text that is far from a pattern must be refused quickly and briefly: a
// request body may be a megabyte of anything (NFR-8 in spirit: nothing a
// client sends can stall the server).
func TestParsePatternBoundsItsWork(t *testing.T) {
	const size = 1 << 20
	for name, text := range map[string]string{
		"opening braces":         strings.Repeat("{", size),
		"closing braces":         strings.Repeat("}", size),
		"unclosed variables":     "x/" + strings.Repeat("{a", size/2),
		"unclosed named classes": "x/{v:regex([" + strings.Repeat("[:", size/2),
		"unclosed quotes":        "x/{v:regex(" + strings.Repeat(`\Q`, size/2),
		"nested groups":          "x/{v:regex(" + strings.Repeat("(", size),
		"variables in a row":     "x/" + strings.Repeat("{a:regex([[:)}", size/14),
		"slashes":                strings.Repeat("/", size),
		"letters":                strings.Repeat("a", size),
		"invalid UTF-8":          strings.Repeat("\xff", size),
	} {
		p, tokens, problems := scanPattern(text, true)
		if len(p.Segments()) != 0 || len(problems) == 0 || len(problems) > maxPatternProblems || tokens != nil {
			t.Errorf("%s: %d problems, %d tokens; want between 1 and %d problems and no tokens",
				name, len(problems), len(tokens), maxPatternProblems)
		}
		if problems[0].Code != CodePatternTooLong && problems[len(problems)-1].Code != CodePatternTooLong {
			t.Errorf("%s: problems = %+v, want %s among them", name, problems, CodePatternTooLong)
		}
		checkProblems(t, text, FieldPattern, problems)
		if got := Shape(text); got != text {
			t.Errorf("%s: Shape changed the text", name)
		}
	}
	// Within the length that is read, many mistakes are still few problems.
	for _, text := range []string{strings.Repeat("{", 200), "x/" + strings.Repeat("{a", 99), strings.Repeat("{}", 100), "x/" + strings.Repeat("}{", 99)} {
		_, problems := ParsePattern(text)
		if len(problems) == 0 || len(problems) > maxPatternProblems {
			t.Errorf("ParsePattern(%q) reported %d problems, want 1 to %d", text, len(problems), maxPatternProblems)
		}
		checkProblems(t, text, FieldPattern, problems)
	}
}

// A form asks about a pattern while it is being typed: the name it is going
// to give the link, and the variables that can already be read.
func TestPatternSoFar(t *testing.T) {
	tests := []struct {
		pattern   string
		name      string
		variables []string
		text      string // the readable segments, canonical
	}{
		// A valid pattern: exactly what ParsePattern returns.
		{"Reports/{month:INT}/{day}", "reports", []string{"month", "day"}, "reports/{month:int}/{day}"},
		{"roadmap", "roadmap", nil, "roadmap"},
		// Not finished: what is well-formed so far.
		{"reports/{month:int}/{day", "reports", []string{"month"}, "reports/{month:int}"},
		{"reports/{month:nope}/{day:int}", "reports", []string{"day"}, "reports/{day:int}"},
		{"gh/", "gh", nil, "gh"},
		// Of two variables with one name, the first.
		{"gh/{a:int}/{A}", "gh", []string{"a"}, "gh/{a:int}"},
		// No name: the first segment is not a literal fit to be one, and a
		// later literal is never taken for it.
		{"", "", nil, ""},
		{"{repo}/x", "", []string{"repo"}, "{repo}/x"},
		{"re ports/x/{n}", "", []string{"n"}, "x/{n}"},
		{"favicon.ico/{n}", "", []string{"n"}, "favicon.ico/{n}"},
		{"-x/{n}", "", []string{"n"}, "{n}"},
		{strings.Repeat("a", 201) + "/{n}", "", []string{"n"}, strings.Repeat("a", 201) + "/{n}"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			name, readable := PatternSoFar(tt.pattern)
			if name != tt.name || !slices.Equal(readable.Variables(), tt.variables) || readable.String() != tt.text {
				t.Errorf("PatternSoFar(%q) = %q, variables %q, text %q; want %q, %q, %q",
					tt.pattern, name, readable.Variables(), readable.String(), tt.name, tt.variables, tt.text)
			}
			if parsed, problems := ParsePattern(tt.pattern); len(problems) == 0 {
				if name != parsed.Name() || !reflect.DeepEqual(readable, parsed) {
					t.Errorf("PatternSoFar(%q) differs from ParsePattern for a valid pattern", tt.pattern)
				}
			}
			// The variables it lists are the ones Accepts answers for.
			for _, v := range tt.variables {
				if _, declared := Accepts(readable, Template{}, v, "1"); !declared {
					t.Errorf("Accepts does not know %q of %q", v, tt.pattern)
				}
			}
		})
	}
}

// fits is the oracle of the tests and fuzz targets for PRD 5.5, step 3,
// written apart from Match on purpose: it reports whether the request
// segments (as returned by SplitPath) fit the pattern on its own: as many segments, every literal equal ignoring
// case, every value accepted by its variable's type (PRD 5.5, step 3). It
// knows nothing of the destination, so the rule of NFR-10 is not applied
// here; CompiledLink.Match is the complete rule, and the only one outside the
// tests (NFR-22).
func (p Pattern) fits(segments []string) bool {
	if len(p.segments) == 0 || len(p.segments) != len(segments) {
		return false
	}
	for i, seg := range p.segments {
		if seg.Type != nil {
			if !seg.Type.Accepts(segments[i]) {
				return false
			}
		} else if !equalFoldASCII(seg.Literal, segments[i]) {
			return false
		}
	}
	return true
}

// A pattern is printed wherever its link is shown, so it holds no raw control
// characters, inside regex(…) either; the expression writes them as escapes.
// A pattern that is already stored is still read.
func TestRegexRefusesRawControlCharacters(t *testing.T) {
	for name, source := range map[string]string{
		"line feed": "a\nb", "carriage return": "a\rb", "tab": "a\tb", "NUL": "a\x00b", "escape": "\x1bx",
		"next line": "a\u0085b", "line separator": "a\u2028b", "paragraph separator": "a\u2029b", "delete": "a\x7fb",
	} {
		pattern := "x/{v:regex(" + source + ")}"
		_, problems := ParsePattern(pattern)
		if len(problems) != 1 || problems[0].Code != CodePatternBadCharacter ||
			problems[0].Start != len("x/{v:regex(") || problems[0].End != len(pattern)-2 ||
			!strings.Contains(problems[0].Message, "escape") {
			t.Errorf("%s: ParsePattern(%q) problems = %+v, want %s on the expression", name, pattern, problems, CodePatternBadCharacter)
		}
		if strings.ContainsAny(problemsText(problems), "\n\r\t\x00\x1b") {
			t.Errorf("%s: a message repeats the control character: %q", name, problemsText(problems))
		}
		if _, ok := StoredPattern(pattern); !ok {
			t.Errorf("%s: the stored pattern %q can no longer be read", name, pattern)
		}
	}
	// The same characters written as escapes, and a plain space, are fine.
	for _, source := range []string{`a\nb`, `a\tb`, `\x{2028}`, `[a-z ]+`, `a\x00b`} {
		mustParse(t, "x/{v:regex("+source+")}")
	}
}

func problemsText(problems []Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString(p.Message)
	}
	return b.String()
}
