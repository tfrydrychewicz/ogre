package linkmodel

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits on a pattern (PRD 5.2). They keep matching cheap and predictable no
// matter what is saved.
const (
	MaxPatternLength   = 200 // characters
	MaxPatternSegments = 8
)

// Problem codes reported by ValidatePattern. They are part of the API: clients
// and tests key on them, so they must not change once released.
const (
	CodePatternEmpty                = "pattern.empty"
	CodePatternTooLong              = "pattern.too_long"
	CodePatternTooManySegments      = "pattern.too_many_segments"
	CodePatternEmptySegment         = "pattern.empty_segment"
	CodePatternBadStart             = "pattern.bad_start"
	CodePatternBadCharacter         = "pattern.bad_character"
	CodePatternVariablesUnsupported = "pattern.variables_unsupported"
	CodePatternReservedName         = "pattern.reserved_name"
)

// SegmentKind says what one segment of a pattern is.
type SegmentKind int

// The kinds of segment. Literals are the only kind until variables arrive in
// L2; the kind exists now so that a parsed pattern keeps its shape when they
// do.
const (
	// SegmentLiteral is fixed text that must equal the request's segment,
	// ignoring case.
	SegmentLiteral SegmentKind = iota
)

// Segment is one "/"-separated part of a parsed pattern.
type Segment struct {
	Kind SegmentKind
	// Literal is the segment's text in lowercase, which is how literals are
	// saved and compared.
	Literal string
}

// Pattern is a route pattern after parsing: what ParsePattern returns, what
// the index keeps for matching, and the one place that knows a pattern's
// canonical text and shape. The zero Pattern has no segments and matches
// nothing.
type Pattern struct {
	segments []Segment
}

// ParsePattern checks a route pattern, the text that follows "go/", and
// returns it parsed.
//
// L1 first cut: every segment must be a literal. A literal uses ASCII letters,
// digits, "-", "_" and ".", and begins with a letter or digit (PRD 5.2). A
// pattern has at most MaxPatternSegments segments and MaxPatternLength
// characters, no empty segments, and a first segment (the link's name) that is
// not reserved. Variables arrive in L2; until then a "{" is reported as
// CodePatternVariablesUnsupported and nothing after it is examined, because a
// variable may legitimately contain "/".
//
// When the pattern is valid it returns the parsed pattern and no problems.
// Otherwise it returns the zero Pattern and the problems ordered by position,
// each with the byte range it concerns.
func ParsePattern(pattern string) (Pattern, []Problem) {
	var problems []Problem
	add := func(code, message string, start, end int) {
		problems = append(problems, Problem{
			Severity: SeverityError, Field: FieldPattern,
			Code: code, Message: message, Start: start, End: end,
		})
	}

	if pattern == "" {
		add(CodePatternEmpty, "Enter a pattern, such as roadmap or gh/new.", 0, 0)
		return Pattern{}, problems
	}
	if over := offsetOfRune(pattern, MaxPatternLength); over >= 0 {
		add(CodePatternTooLong, fmt.Sprintf(
			"A pattern can have at most %d characters; this one has %d.",
			MaxPatternLength, utf8.RuneCountInString(pattern)), over, len(pattern))
	}
	if open := strings.IndexByte(pattern, '{'); open >= 0 {
		end := len(pattern)
		if closing := strings.IndexByte(pattern[open:], '}'); closing >= 0 {
			end = open + closing + 1
		}
		add(CodePatternVariablesUnsupported,
			"Variables such as {name} are not supported yet. Use only fixed text.", open, end)
		return Pattern{}, byPosition(problems)
	}

	var segments []Segment
	for start, count := 0, 0; start <= len(pattern); count++ {
		end := len(pattern)
		if slash := strings.IndexByte(pattern[start:], '/'); slash >= 0 {
			end = start + slash
		}
		if count == MaxPatternSegments {
			add(CodePatternTooManySegments, fmt.Sprintf(
				"A pattern can have at most %d segments.", MaxPatternSegments), start, len(pattern))
			break
		}
		seg := pattern[start:end]
		if p, ok := checkLiteral(pattern, start, end); !ok {
			if len(problems) == 0 || problems[len(problems)-1] != p {
				problems = append(problems, p)
			}
		}
		segments = append(segments, Segment{Kind: SegmentLiteral, Literal: NormalizeName(seg)})
		start = end + 1
	}

	if first := segments[0].Literal; ReservedName(first) {
		add(CodePatternReservedName, fmt.Sprintf(
			"%q can't be a link name, because browsers request it on their own.", first), 0, len(first))
	}

	if len(problems) > 0 {
		return Pattern{}, byPosition(problems)
	}
	return Pattern{segments: segments}, nil
}

// ValidatePattern is ParsePattern for callers that only need the literal
// segments: it returns them in lowercase when the pattern is valid, and no
// segments and the problems otherwise.
func ValidatePattern(pattern string) (segments []string, problems []Problem) {
	parsed, problems := ParsePattern(pattern)
	if len(problems) > 0 {
		return nil, problems
	}
	segments = make([]string, len(parsed.segments))
	for i, seg := range parsed.segments {
		segments[i] = seg.Literal
	}
	return segments, nil
}

// Segments returns the pattern's segments in order. The slice is the
// pattern's own: read it, never modify it.
func (p Pattern) Segments() []Segment { return p.segments }

// Name returns the link name the pattern belongs to: its first segment
// (PRD 5.1). It is "" for the zero Pattern.
func (p Pattern) Name() string {
	if len(p.segments) == 0 {
		return ""
	}
	return p.segments[0].Literal
}

// Canonical returns the text the pattern is saved as: its segments in
// lowercase, joined with "/". Parsing the canonical text gives the same
// pattern back.
func (p Pattern) Canonical() string {
	parts := make([]string, len(p.segments))
	for i, seg := range p.segments {
		parts[i] = seg.Literal
	}
	return strings.Join(parts, "/")
}

// Shape returns the pattern with everything removed that does not affect
// which requests it matches (PRD 5.5). Two routes of one link with the same
// shape would compete for the same requests, so a link may hold each shape
// once.
//
// A literal-only pattern is its own shape. L2 replaces each variable's name
// here, which is why callers must ask for the shape instead of assuming it.
func (p Pattern) Shape() string { return p.Canonical() }

// Matches reports whether the request segments (as returned by SplitPath)
// fit the pattern: it has as many segments as the request and each literal
// equals the request's segment at that position, ignoring case (PRD 5.5,
// step 3). The zero Pattern matches nothing.
//
// It runs for every resolved request and does not allocate.
func (p Pattern) Matches(segments []string) bool {
	if len(p.segments) == 0 || len(p.segments) != len(segments) {
		return false
	}
	for i, seg := range p.segments {
		if !equalFoldASCII(seg.Literal, segments[i]) {
			return false
		}
	}
	return true
}

// Shape returns the shape of a saved pattern, for a store to keep beside it
// so the database itself refuses two routes of one link that would match the
// same requests. The rule is Pattern.Shape; this is its form for callers that
// hold only the text.
//
// Text that does not parse is returned unchanged: a store must never fail or
// guess because of it, and identical text is then still refused twice.
func Shape(pattern string) string {
	parsed, problems := ParsePattern(pattern)
	if len(problems) > 0 {
		return pattern
	}
	return parsed.Shape()
}

// byPosition orders problems by where they start, so a form can list them in
// reading order. Problems that start at the same place keep the order in
// which they were found.
func byPosition(problems []Problem) []Problem {
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Start < problems[j].Start })
	return problems
}

// checkLiteral checks the segment pattern[start:end] against the rules for a
// literal and returns the first problem found in it. One problem per segment
// is enough to act on and keeps a pasted URL from producing a wall of errors.
func checkLiteral(pattern string, start, end int) (Problem, bool) {
	problem := func(code, message string, from, to int) (Problem, bool) {
		return Problem{
			Severity: SeverityError, Field: FieldPattern,
			Code: code, Message: message, Start: from, End: to,
		}, false
	}

	seg := pattern[start:end]
	if seg == "" {
		// Point at the slash that creates the empty segment, so there is
		// something to underline: the one after it, or for a trailing slash
		// the one before it.
		from, to := start, start+1
		if start == len(pattern) {
			from, to = start-1, start
		}
		return problem(CodePatternEmptySegment,
			"A pattern can't start or end with a slash, or have two slashes in a row.", from, to)
	}
	for i, r := range seg {
		at := start + i
		switch {
		case isLiteralStart(r):
		case i == 0 && isLiteralRune(r):
			return problem(CodePatternBadStart, fmt.Sprintf(
				"A segment must begin with a letter or digit, not %q.", r), at, at+1)
		case isLiteralRune(r):
		default:
			// Measure the bytes actually consumed: an invalid byte and a real
			// U+FFFD both arrive here as utf8.RuneError.
			_, size := utf8.DecodeRuneInString(seg[i:])
			return problem(CodePatternBadCharacter, fmt.Sprintf(
				"%q can't be used here. A pattern uses the letters a-z, digits, \"-\", \"_\" and \".\".",
				r), at, at+size)
		}
	}
	return Problem{}, true
}

// isLiteralStart reports whether r may begin a literal: an ASCII letter or
// digit. Beginning with a letter or digit keeps "." and ".." from ever being
// names and leaves "-" free to mean something at the start later on.
func isLiteralStart(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
}

// isLiteralRune reports whether r may appear in a literal.
func isLiteralRune(r rune) bool {
	return isLiteralStart(r) || r == '-' || r == '_' || r == '.'
}

// offsetOfRune returns the byte offset of the rune with index n in s, or -1
// when s has at most n runes. It turns a limit counted in characters into the
// byte offset where the excess begins.
func offsetOfRune(s string, n int) int {
	if len(s) <= n {
		return -1 // fewer bytes than the limit means fewer runes too
	}
	count := 0
	for i := range s {
		if count == n {
			return i
		}
		count++
	}
	return -1
}
