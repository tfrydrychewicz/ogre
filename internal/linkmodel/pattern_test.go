package linkmodel

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidatePatternValid(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{"one segment", "roadmap", []string{"roadmap"}},
		{"single character", "a", []string{"a"}},
		{"single digit", "7", []string{"7"}},
		{"begins with a digit", "2fa", []string{"2fa"}},
		{"two segments", "gh/new", []string{"gh", "new"}},

		// Saved in lowercase.
		{"upper case is folded", "Roadmap", []string{"roadmap"}},
		{"every segment is folded", "GH/New/PR", []string{"gh", "new", "pr"}},

		// Q5: separators are kept and significant.
		{"hyphen", "on-call", []string{"on-call"}},
		{"underscore", "on_call", []string{"on_call"}},
		{"no separator", "oncall", []string{"oncall"}},
		{"dot", "v1.2", []string{"v1.2"}},
		{"mixed case with a hyphen", "On-Call", []string{"on-call"}},
		{"separators at the end", "a-/b_/c.", []string{"a-", "b_", "c."}},
		{"doubled separators", "a--b/c__d/e..f", []string{"a--b", "c__d", "e..f"}},

		// Limits, at the boundary.
		{"eight segments", "a/b/c/d/e/f/g/h", []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
		{"200 characters", strings.Repeat("a", 200), []string{strings.Repeat("a", 200)}},

		// Close to reserved, but not reserved.
		{"favicon without extension", "favicon", []string{"favicon"}},
		{"reserved name in a later segment", "docs/robots.txt", []string{"docs", "robots.txt"}},
		{"apple-touch-icon without .png", "apple-touch-icon", []string{"apple-touch-icon"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := ValidatePattern(tt.pattern)
			if len(problems) != 0 {
				t.Fatalf("ValidatePattern(%q) problems = %+v, want none", tt.pattern, problems)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ValidatePattern(%q) segments = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

// Decision Q5 at the validation level: the three spellings are three
// different names.
func TestValidatePatternSeparatorsAreSignificant(t *testing.T) {
	names := map[string]bool{}
	for _, p := range []string{"on-call", "on_call", "oncall"} {
		segments, problems := ValidatePattern(p)
		if len(problems) != 0 {
			t.Fatalf("ValidatePattern(%q) problems = %+v", p, problems)
		}
		names[segments[0]] = true
	}
	if len(names) != 3 {
		t.Errorf("got %d distinct names %v, want 3", len(names), names)
	}
}

func TestValidatePatternProblems(t *testing.T) {
	type want struct {
		code       string
		start, end int
	}
	long := strings.Repeat("a", 201)
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

		// A segment begins with a letter or digit.
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
		{"closing brace alone", "a}", []want{{CodePatternBadCharacter, 1, 2}}},
		{"one problem per segment", "a!b!c/d?e", []want{
			{CodePatternBadCharacter, 1, 2}, {CodePatternBadCharacter, 7, 8}}},
		{"non-ASCII letter", "café", []want{{CodePatternBadCharacter, 3, 5}}},
		{"non-ASCII first letter", "über", []want{{CodePatternBadCharacter, 0, 2}}},
		{"non-ASCII in a later segment", "gh/日本", []want{{CodePatternBadCharacter, 3, 6}}},
		{"kelvin sign", "K", []want{{CodePatternBadCharacter, 0, 3}}},
		{"invalid UTF-8", "a\xffb", []want{{CodePatternBadCharacter, 1, 2}}},
		{"replacement character", "a�b", []want{{CodePatternBadCharacter, 1, 4}}},
		{"NUL", "a\x00b", []want{{CodePatternBadCharacter, 1, 2}}},
		{"newline", "a\nb", []want{{CodePatternBadCharacter, 1, 2}}},

		// Variables arrive in L2.
		{"variable", "gh/{repo}", []want{{CodePatternVariablesUnsupported, 3, 9}}},
		{"typed variable", "gh/{num:int}/x", []want{{CodePatternVariablesUnsupported, 3, 12}}},
		{"unclosed variable", "gh/{repo", []want{{CodePatternVariablesUnsupported, 3, 8}}},
		{"variable first", "{name}", []want{{CodePatternVariablesUnsupported, 0, 6}}},
		{"brace inside a segment", "a{b", []want{{CodePatternVariablesUnsupported, 1, 3}}},
		{"regex with a slash is not split", "d/{x:regex(a/b)}/e", []want{{CodePatternVariablesUnsupported, 2, 16}}},

		// Limits.
		{"201 characters", long, []want{{CodePatternTooLong, 200, 201}}},
		{"too long, counted in characters", strings.Repeat("a", 199) + "éé", []want{
			{CodePatternBadCharacter, 199, 201}, {CodePatternTooLong, 201, 203}}},
		{"too long and a variable", long + "/{x}", []want{
			{CodePatternTooLong, 200, 205}, {CodePatternVariablesUnsupported, 202, 205}}},
		{"too long with a variable before the limit", strings.Repeat("a", 100) + "{" + strings.Repeat("a", 100), []want{
			{CodePatternVariablesUnsupported, 100, 201}, {CodePatternTooLong, 200, 201}}},
		{"nine segments", "a/b/c/d/e/f/g/h/i", []want{{CodePatternTooManySegments, 16, 17}}},
		{"eleven segments", "a/b/c/d/e/f/g/h/i/j/k", []want{{CodePatternTooManySegments, 16, 21}}},
		{"eight segments and a trailing slash", "a/b/c/d/e/f/g/h/", []want{{CodePatternTooManySegments, 16, 16}}},

		// Reserved names (plan 3.3), ignoring case, first segment only.
		{"favicon.ico", "favicon.ico", []want{{CodePatternReservedName, 0, 11}}},
		{"favicon.ico in upper case", "FAVICON.ICO", []want{{CodePatternReservedName, 0, 11}}},
		{"robots.txt", "robots.txt", []want{{CodePatternReservedName, 0, 10}}},
		{"sitemap.xml", "sitemap.xml", []want{{CodePatternReservedName, 0, 11}}},
		{"apple-touch-icon.png", "apple-touch-icon.png", []want{{CodePatternReservedName, 0, 20}}},
		{"apple-touch-icon variant", "apple-touch-icon-120x120.png", []want{{CodePatternReservedName, 0, 28}}},
		{"reserved with a sub-path", "robots.txt/x", []want{{CodePatternReservedName, 0, 10}}},
		{".well-known", ".well-known/x", []want{
			{CodePatternBadStart, 0, 1}, {CodePatternReservedName, 0, 11}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, problems := ValidatePattern(tt.pattern)
			if segments != nil {
				t.Errorf("ValidatePattern(%q) returned segments %q for an invalid pattern", tt.pattern, segments)
			}
			var got []want
			for _, p := range problems {
				got = append(got, want{p.Code, p.Start, p.End})
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ValidatePattern(%q) problems =\n  %+v\nwant\n  %+v", tt.pattern, got, tt.want)
			}
			checkProblems(t, tt.pattern, FieldPattern, problems)
		})
	}
}

// checkProblems asserts what holds for every problem regardless of its code:
// it names its field, is an error, has a readable message, and its offsets
// are ordered byte offsets inside the input.
func checkProblems(t *testing.T, input, field string, problems []Problem) {
	t.Helper()
	prev := 0
	for _, p := range problems {
		if p.Severity != SeverityError {
			t.Errorf("problem %s: severity = %q, want %q", p.Code, p.Severity, SeverityError)
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
		if p.Start < 0 || p.Start > p.End || p.End > len(input) {
			t.Errorf("problem %s: range [%d,%d) is outside the input of %d bytes", p.Code, p.Start, p.End, len(input))
		}
		if p.Start < prev {
			t.Errorf("problem %s: start %d comes before the previous problem's %d", p.Code, p.Start, prev)
		}
		prev = p.Start
	}
}

func FuzzValidatePattern(f *testing.F) {
	for _, seed := range []string{
		"", "roadmap", "On-Call", "gh/new", "a//b", "/a", "a/", "-a", ".a", "..",
		"café", "a\xffb", "gh/{repo}", "gh/{x:regex(a/b)}", "{", "}", "favicon.ico",
		"a/b/c/d/e/f/g/h/i", strings.Repeat("a", 201), strings.Repeat("é", 150), "a b", "%",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		segments, problems := ValidatePattern(pattern)
		checkProblems(t, pattern, FieldPattern, problems)

		if len(problems) > 0 {
			if segments != nil {
				t.Fatalf("ValidatePattern(%q) returned segments %q with problems", pattern, segments)
			}
			return
		}
		// A pattern without problems is exactly its segments, lowercased, and
		// every segment obeys the literal rules.
		if got, want := strings.Join(segments, "/"), NormalizeName(pattern); got != want {
			t.Fatalf("ValidatePattern(%q) segments join to %q, want %q", pattern, got, want)
		}
		if len(segments) == 0 || len(segments) > MaxPatternSegments {
			t.Fatalf("ValidatePattern(%q) accepted %d segments", pattern, len(segments))
		}
		if utf8.RuneCountInString(pattern) > MaxPatternLength {
			t.Fatalf("ValidatePattern accepted a pattern of %d characters", utf8.RuneCountInString(pattern))
		}
		if ReservedName(segments[0]) {
			t.Fatalf("ValidatePattern(%q) accepted a reserved name", pattern)
		}
		for _, seg := range segments {
			if seg == "" || !isLiteralStart(rune(seg[0])) {
				t.Fatalf("ValidatePattern(%q) accepted segment %q", pattern, seg)
			}
			for _, r := range seg {
				if !isLiteralRune(r) || 'A' <= r && r <= 'Z' {
					t.Fatalf("ValidatePattern(%q) accepted %q in segment %q", pattern, r, seg)
				}
			}
		}
		// Canonical text is stable: validating it again changes nothing.
		again, problems := ValidatePattern(strings.Join(segments, "/"))
		if len(problems) != 0 || !slices.Equal(again, segments) {
			t.Fatalf("canonical form of %q does not validate to itself: %q, %+v", pattern, again, problems)
		}
		// A valid pattern is a path that resolves to itself.
		parsed, problems := ParsePattern(pattern)
		split, err := SplitPath(pattern)
		if err != nil || len(problems) != 0 || !parsed.Matches(split) {
			t.Fatalf("valid pattern %q does not match its own path (split %q, err %v)", pattern, split, err)
		}
		// The parsed form agrees with the segments, and its canonical text
		// and shape are the same pattern again.
		if parsed.Canonical() != strings.Join(segments, "/") || parsed.Name() != segments[0] {
			t.Fatalf("ParsePattern(%q): canonical %q, name %q; want %q, %q",
				pattern, parsed.Canonical(), parsed.Name(), strings.Join(segments, "/"), segments[0])
		}
		if Shape(pattern) != parsed.Shape() || Shape(parsed.Canonical()) != parsed.Shape() {
			t.Fatalf("Shape(%q) = %q, want %q for the text and its canonical form", pattern, Shape(pattern), parsed.Shape())
		}
		if !PossibleName(segments[0]) {
			t.Fatalf("PossibleName(%q) = false for the name of valid pattern %q", segments[0], pattern)
		}
	})
}
