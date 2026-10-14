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
	MaxPatternLength    = 200 // characters
	MaxPatternSegments  = 8
	MaxPatternVariables = 6
)

// Bounds on the work of checking text that is not a pattern. They are not
// rules a user meets: a text has broken MaxPatternLength, or holds a dozen
// mistakes, long before either applies.
const (
	// maxPatternScan is the most bytes of a pattern that are read. A
	// character is at most four bytes, so anything longer is over
	// MaxPatternLength whatever it holds.
	maxPatternScan = 4 * MaxPatternLength
	// maxPatternProblems is how many problems are reported for one pattern
	// before reading stops. Without it "{{{{…" would yield a problem per
	// brace.
	maxPatternProblems = 20
)

// Segment is one "/"-separated part of a parsed pattern: fixed text or one
// variable, never both (PRD 5.2).
type Segment struct {
	// Literal is the segment's fixed text in lowercase, which is how
	// literals are saved and compared. It is empty for a variable.
	Literal string
	// Variable is the variable's name as written. It is empty for a literal.
	Variable string
	// Type is the variable's type; nil for a literal.
	Type VarType
	// Start and End are the byte offsets of the segment in the text that was
	// parsed ([Start, End)), the same unit a Problem uses.
	Start, End int
}

// IsVariable reports whether the segment is a variable rather than fixed
// text.
func (s Segment) IsVariable() bool { return s.Type != nil }

// Token kinds.
const (
	TokenLiteral  = "literal"
	TokenVariable = "variable"
)

// Token is one well-formed segment of a pattern as an editor shows it: where
// it is and what it is. Unlike a Pattern, tokens exist for a pattern with
// mistakes too: the segments that could be read are reported and the others
// are covered by problems, so a form can color what is right while it
// underlines what is wrong.
type Token struct {
	// Kind is "literal" or "variable".
	Kind string
	// Start and End are byte offsets into the pattern text ([Start, End)).
	Start, End int
	// Name is the variable's name; empty for a literal.
	Name string
	// Type is the variable's type name ("string", "int", "number", "regex");
	// empty for a literal.
	Type string
}

// Pattern is a route pattern after parsing: what ParsePattern returns, what
// the index keeps for matching, and the one place that knows a pattern's
// canonical text and shape. The zero Pattern has no segments and matches
// nothing. A Pattern is immutable.
type Pattern struct {
	segments  []Segment
	text      string   // canonical text
	shape     string   // canonical text without variable names
	variables []string // variable names in pattern order
}

// ParsePattern checks a route pattern, the text that follows "go/", and
// returns it parsed (PRD 5.2 and 5.3).
//
// A pattern is segments separated by "/". Each segment is a literal or one
// variable. A literal uses ASCII letters, digits, "-", "_" and ".", and
// begins with a letter or digit. A variable is {name} or {name:type}: the
// name uses ASCII letters, digits and "_" and begins with a letter; the type
// is string, int, number or regex(…), written in any case. The first segment
// is a literal, the link's name, and may not be a reserved name. Variable
// names are unique within the pattern, ignoring case. At most
// MaxPatternSegments segments, MaxPatternVariables variables and
// MaxPatternLength characters.
//
// The pattern is not split on "/": inside regex(…) the expression runs to
// the parenthesis that closes "regex(", so it may itself contain "/", "{",
// "}" and ")" (see regexEnd).
//
// When the pattern is valid it returns the parsed pattern and no problems.
// Otherwise it returns the zero Pattern and the problems ordered by position,
// each with the byte range it concerns. Every problem is an error.
func ParsePattern(pattern string) (Pattern, []Problem) {
	p, _, problems := scanPattern(pattern, true)
	return p, problems
}

// PatternTokens returns the well-formed segments of a pattern as tokens,
// whether or not the pattern as a whole is valid.
func PatternTokens(pattern string) []Token {
	_, tokens, _ := scanPattern(pattern, true)
	return tokens
}

// PatternSoFar reads a pattern that is still being written and may not be
// valid yet, for a form that answers while the user types: which name is
// this link going to have, and would these variables accept that value?
//
// name is the link's name when the first segment is a well-formed literal
// that can be a name, and "" otherwise. readable holds the well-formed
// segments (the ones PatternTokens lists; of two variables with one name,
// the first): ask it for Variables and pass it to Accepts and
// ParseDestination. It is not the pattern: never save it, match with it or
// take its Name, String or Shape for the real ones.
//
// For a valid pattern, name and readable are exactly ParsePattern's result
// and its Name.
func PatternSoFar(pattern string) (name string, readable Pattern) {
	r := scan(pattern, true)
	if len(r.problems) == 0 {
		return r.pattern.Name(), r.pattern
	}
	return r.name, newPattern(r.readable)
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

// String returns the canonical text, the form a pattern is saved in:
// literals in lowercase, a variable as {name} or {name:type} with the type
// as its Format writes it, joined with "/". Parsing the canonical text gives
// an equal pattern back.
func (p Pattern) String() string { return p.text }

// Shape returns the canonical text with the variable names removed: "gh/{}",
// "gh/{:int}". Two patterns with the same shape match exactly the same
// requests, so a link may hold each shape once (PRD 5.5). Types and regular
// expressions are part of the shape; a literal-only pattern is its own shape.
func (p Pattern) Shape() string { return p.shape }

// Variables returns the names of the pattern's variables in pattern order.
// The slice is the pattern's own: read it, never modify it.
func (p Pattern) Variables() []string { return p.variables }

// variable returns the segment of the named variable.
func (p Pattern) variable(name string) (Segment, bool) {
	for _, seg := range p.segments {
		if seg.Type != nil && seg.Variable == name {
			return seg, true
		}
	}
	return Segment{}, false
}

// StoredPattern reads a pattern that was saved earlier. ok is false when the
// text is not a pattern.
//
// It is the one way to read stored text, for the index (Compile), the API
// (RouteVariables), the shape column (Shape) and the store: the rules that
// only bind a pattern being saved are not applied again, so a route that was
// saved keeps its meaning when such a rule is added or tightened later, and
// under a later Go release (see newRegexType). Use ParsePattern for text
// that someone is about to save.
func StoredPattern(pattern string) (parsed Pattern, ok bool) {
	parsed, _, problems := scanPattern(pattern, false)
	if len(problems) > 0 {
		return Pattern{}, false
	}
	return parsed, true
}

// Shape returns the shape of a saved pattern, for a store to keep beside it
// so the database itself refuses two routes of one link that would match the
// same requests. The rule is Pattern.Shape; this is its form for callers that
// hold only the text.
//
// Text that does not parse is returned unchanged: a store must never fail or
// guess because of it, and identical text is then still refused twice.
func Shape(pattern string) string {
	parsed, ok := StoredPattern(pattern)
	if !ok {
		return pattern
	}
	return parsed.Shape()
}

// scanner reads one pattern. It is a tokenizer, not a split on "/": where a
// segment ends depends on what it contains.
type scanner struct {
	text     string
	strict   bool // apply the rules that only bind a pattern being saved
	problems []Problem
}

// full reports whether enough problems were found to stop reading.
func (s *scanner) full() bool { return len(s.problems) >= maxPatternProblems }

func (s *scanner) add(code, message string, start, end int) {
	if s.full() {
		return
	}
	p := Problem{
		Severity: SeverityError, Field: FieldPattern,
		Code: code, Message: message, Start: start, End: end,
	}
	// "/" yields the same empty-segment problem from both of its sides.
	if n := len(s.problems); n > 0 && s.problems[n-1] == p {
		return
	}
	s.problems = append(s.problems, p)
}

// scanPattern is ParsePattern with its two extras: the tokens, and the
// choice of whether the pattern is being saved (strict) or read back from
// the store. A stored pattern is not held to MaxRegexProgram, a limit that
// depends on the Go release (see newRegexType).
func scanPattern(pattern string, strict bool) (Pattern, []Token, []Problem) {
	r := scan(pattern, strict)
	return r.pattern, r.tokens, r.problems
}

// scanned is everything one pass over a pattern's text finds out.
type scanned struct {
	pattern  Pattern   // the zero Pattern unless there are no problems
	tokens   []Token   // the well-formed segments
	problems []Problem // ordered by position
	// What can be made of a pattern with problems (PatternSoFar): the name,
	// when the first segment is a literal fit to be one, and the well-formed
	// segments, without a variable whose name was already taken.
	name     string
	readable []Segment
}

func scan(pattern string, strict bool) scanned {
	s := &scanner{text: pattern, strict: strict}
	if pattern == "" {
		s.add(CodePatternEmpty, "Enter a pattern, such as roadmap or gh/{repo}.", 0, 0)
		return scanned{problems: s.problems}
	}
	if over := offsetOfRune(pattern, MaxPatternLength); over >= 0 {
		s.add(CodePatternTooLong, fmt.Sprintf(
			"A pattern can have at most %d characters; this one has %d.",
			MaxPatternLength, utf8.RuneCountInString(pattern)), over, len(pattern))
	}

	if len(pattern) > maxPatternScan {
		// Far beyond the limit: say so and read no further. The body of a
		// request may be a megabyte, and nothing in it could make this a
		// pattern.
		return scanned{problems: s.problems}
	}

	var (
		segments  []Segment
		readable  []Segment
		tokens    []Token
		variables []string
		name      string
		seen      = map[string]bool{} // lowercase variable names
	)
	for start, count := 0, 0; start <= len(pattern); count++ {
		if count == MaxPatternSegments {
			s.add(CodePatternTooManySegments, fmt.Sprintf(
				"A pattern can have at most %d segments.", MaxPatternSegments), start, len(pattern))
			break
		}
		seg, end, kind, ok := s.segment(start)
		if s.full() {
			break // enough to act on; see maxPatternProblems
		}

		if count == 0 && kind == partLiteral {
			// Checked even when the literal has another problem, so that
			// ".well-known" is told both things at once.
			first := NormalizeName(pattern[start:end])
			switch {
			case ReservedName(first):
				s.add(CodePatternReservedName, fmt.Sprintf(
					"%q can't be a link name, because browsers request it on their own.", first), start, end)
			case ok && PossibleName(first):
				name = first
			}
		}
		if count == 0 && kind == partVariable {
			s.add(CodePatternVariableFirst,
				"A pattern must begin with the link's name, which is fixed text. It can't begin with a variable.",
				start, end)
		}
		repeated := false
		if ok && kind == partVariable {
			switch lower := NormalizeName(seg.Variable); {
			case seen[lower]:
				repeated = true
				s.add(CodePatternDuplicateVariable, fmt.Sprintf(
					"This pattern already has a variable named %s. Each variable needs its own name; upper and lower case don't make two names.",
					seg.Variable), start+1, start+1+len(seg.Variable))
			case len(variables) == MaxPatternVariables:
				s.add(CodePatternTooManyVariables, fmt.Sprintf(
					"A pattern can have at most %d variables.", MaxPatternVariables), start, end)
				variables = append(variables, seg.Variable) // report the limit once
			default:
				seen[lower] = true
				variables = append(variables, seg.Variable)
			}
		}
		if ok {
			token := Token{Kind: TokenLiteral, Start: start, End: end}
			if kind == partVariable {
				token = Token{Kind: TokenVariable, Start: start, End: end, Name: seg.Variable, Type: seg.Type.Name()}
			}
			tokens = append(tokens, token)
			if !repeated {
				readable = append(readable, seg)
			}
		}
		segments = append(segments, seg)
		start = end + 1
	}

	if len(s.problems) > 0 {
		return scanned{tokens: tokens, problems: byPosition(s.problems), name: name, readable: readable}
	}
	return scanned{pattern: newPattern(segments), tokens: tokens}
}

// newPattern builds a Pattern from valid segments, deriving its canonical
// text and shape once so that neither is rebuilt for every comparison.
//
// Every link's routes are parsed again at startup and held for the life of
// the process (package index), so the two texts are built without slack: a
// pattern made only of literals, as most are, is its own shape and shares
// one string with it, and the builders are sized before they are filled
// (docs/perf.md, NFR-5).
func newPattern(segments []Segment) Pattern {
	p := Pattern{segments: segments}
	size := max(0, len(segments)-1) // the slashes
	for _, seg := range segments {
		size += len(seg.Literal)
		if seg.Type != nil {
			size += len(seg.Variable) + len(seg.Type.Format()) + 3 // "{", ":", "}"
		}
	}
	var text strings.Builder
	text.Grow(size)
	for i, seg := range segments {
		if i > 0 {
			text.WriteByte('/')
		}
		if seg.Type == nil {
			text.WriteString(seg.Literal)
			continue
		}
		p.variables = append(p.variables, seg.Variable)
		text.WriteByte('{')
		text.WriteString(seg.Variable)
		writeFormat(&text, seg.Type)
		text.WriteByte('}')
	}
	p.text, p.shape = text.String(), text.String()
	if len(p.variables) == 0 {
		return p
	}
	var shape strings.Builder
	shape.Grow(size)
	for i, seg := range segments {
		if i > 0 {
			shape.WriteByte('/')
		}
		if seg.Type == nil {
			shape.WriteString(seg.Literal)
			continue
		}
		shape.WriteByte('{')
		writeFormat(&shape, seg.Type)
		shape.WriteByte('}')
	}
	p.shape = shape.String()
	return p
}

// writeFormat writes ":" and the type's format, or nothing for the string
// type, which a pattern writes without one.
func writeFormat(b *strings.Builder, t VarType) {
	if format := t.Format(); format != "" {
		b.WriteByte(':')
		b.WriteString(format)
	}
}

// What a segment's text turned out to be.
const (
	partNone     = iota // nothing between two slashes
	partLiteral         // fixed text
	partVariable        // one variable
	partMixed           // several of them without a slash between
)

// segment reads the segment that begins at start. It returns the segment,
// the offset where it ends (a "/" or the end of the text), what kind of
// segment the text is, and whether it is well-formed. Problems are added to
// the scanner; the returned Segment is only meaningful when ok is true.
func (s *scanner) segment(start int) (seg Segment, end, kind int, ok bool) {
	text := s.text
	var (
		parts       int
		variable    Segment
		variables   int
		variablesOK = true
		literals    [][2]int // the runs of text that are not variables
		pieces      []string // the parts, to show how to separate them
	)
	i := start
	for i < len(text) && text[i] != '/' && !s.full() {
		parts++
		if text[i] == '{' {
			v, next, fine := s.variable(i)
			variable, variables, variablesOK = v, variables+1, variablesOK && fine
			pieces = append(pieces, text[i:next])
			i = next
			continue
		}
		j := i
		for j < len(text) && text[j] != '/' && text[j] != '{' {
			j++
		}
		literals = append(literals, [2]int{i, j})
		pieces = append(pieces, text[i:j])
		i = j
	}
	end = i

	switch {
	case parts == 0:
		// Point at the slash that creates the empty segment, so there is
		// something to underline: the one after it, or for a trailing slash
		// the one before it.
		from, to := start, start+1
		if start == len(text) {
			from, to = start-1, start
		}
		s.add(CodePatternEmptySegment,
			"A pattern can't start or end with a slash, or have two slashes in a row.", from, to)
		return Segment{}, end, partNone, false
	case parts == 1 && variables == 0:
		ok = s.literal(start, end)
		return Segment{Literal: NormalizeName(text[start:end]), Start: start, End: end}, end, partLiteral, ok
	case parts == 1:
		return variable, end, partVariable, variablesOK
	}
	// A part with a mistake of its own is reported for that mistake alone;
	// what follows is for parts that are each fine and only lack a slash.
	for _, run := range literals {
		if brace := strings.IndexByte(text[run[0]:run[1]], '}'); brace >= 0 {
			// "{n:int}}": the extra brace is the mistake, not a missing slash.
			s.add(CodePatternUnmatchedBrace,
				"This } has no { before it. A variable is written {name}.", run[0]+brace, run[0]+brace+1)
			variablesOK = false
		}
	}
	if variablesOK {
		s.add(CodePatternMixedSegment, fmt.Sprintf(
			"A segment is either fixed text or one variable, not both. Separate them with a slash: %s.",
			quoteForMessage(strings.Join(pieces, "/"))), start, end)
	}
	return Segment{}, end, partMixed, false
}

// literal checks text[start:end] against the rules for a literal, reports
// the first problem in it and returns whether there was none. One problem
// per segment is enough to act on and keeps a pasted URL from producing a
// wall of errors.
func (s *scanner) literal(start, end int) bool {
	seg := s.text[start:end]
	for i, r := range seg {
		at := start + i
		switch {
		case isLiteralStart(r):
		case i == 0 && isLiteralRune(r):
			s.add(CodePatternBadStart, fmt.Sprintf(
				"A segment must begin with a letter or digit, not %q.", r), at, at+1)
			return false
		case isLiteralRune(r):
		case r == '}':
			s.add(CodePatternUnmatchedBrace,
				"This } has no { before it. A variable is written {name}.", at, at+1)
			return false
		default:
			// Measure the bytes actually consumed: an invalid byte and a real
			// U+FFFD both arrive here as utf8.RuneError.
			_, size := utf8.DecodeRuneInString(seg[i:])
			s.add(CodePatternBadCharacter, fmt.Sprintf(
				"%q can't be used here. Fixed text uses the letters a-z, digits, \"-\", \"_\" and \".\".",
				r), at, at+size)
			return false
		}
	}
	return true
}

// variable reads the variable whose "{" is at open. It returns the segment,
// the offset just past the variable (or where reading had to stop) and
// whether the variable is well-formed. Problems are added to the scanner.
func (s *scanner) variable(open int) (seg Segment, next int, ok bool) {
	text := s.text
	unclosed := func(stop int) (Segment, int, bool) {
		s.add(CodePatternUnclosedVariable, "This variable is missing its closing }.", open, stop)
		return Segment{}, stop, false
	}

	// The name runs to the colon or the closing brace.
	nameEnd := open + 1
	for nameEnd < len(text) && !strings.ContainsRune(":}/{", rune(text[nameEnd])) {
		nameEnd++
	}
	if nameEnd == len(text) || text[nameEnd] == '/' || text[nameEnd] == '{' {
		return unclosed(nameEnd)
	}
	name := text[open+1 : nameEnd]

	var (
		typ      VarType = stringType{}
		closing          = nameEnd // offset of the "}"
		typeOK           = true
		rest             = text[nameEnd+1:]
		typeFrom         = nameEnd + 1
	)
	switch {
	case text[nameEnd] == '}':
		// {name}: text, the default type.
	case len(rest) >= len("regex(") && NormalizeName(rest[:len("regex(")]) == "regex(":
		sourceFrom := typeFrom + len("regex(")
		paren := regexEnd(text, sourceFrom)
		if paren < 0 {
			// Where the expression ends is unknown, so nothing after this
			// point can be told apart from it.
			s.add(CodePatternRegexUnbalanced,
				"The parenthesis that closes regex( is missing.", typeFrom, len(text))
			return Segment{}, len(text), false
		}
		if paren+1 >= len(text) || text[paren+1] != '}' {
			stop := paren + 1
			for stop < len(text) && !strings.ContainsRune("}/{", rune(text[stop])) {
				stop++
			}
			if stop == len(text) || text[stop] != '}' {
				return unclosed(stop)
			}
			s.add(CodePatternRegexTrailingText,
				"The variable must end with } right after the parenthesis that closes regex(.", paren+1, stop)
			return Segment{}, stop + 1, false
		}
		closing = paren + 1
		var bad *errRegex
		if typ, bad = newRegexType(text[sourceFrom:paren], s.strict); bad != nil {
			from, to := sourceFrom, paren
			if from == to {
				from, to = typeFrom, paren+1 // "regex()": nothing inside to underline
			}
			s.add(bad.code, bad.message, from, to)
			typeOK = false
		}
	default:
		typeEnd := typeFrom
		for typeEnd < len(text) && !strings.ContainsRune("}/{", rune(text[typeEnd])) {
			typeEnd++
		}
		if typeEnd == len(text) || text[typeEnd] != '}' {
			return unclosed(typeEnd)
		}
		closing = typeEnd
		var message string
		if typ, message = parseType(text[typeFrom:typeEnd]); message != "" {
			from := typeFrom
			if from == typeEnd {
				from = nameEnd // "{n:}": underline the colon
			}
			s.add(CodePatternUnknownType, message, from, typeEnd)
			typeOK = false
		}
	}
	next = closing + 1

	switch {
	case name == "":
		s.add(CodePatternEmptyVariableName,
			"Give the variable a name between the braces, such as {id}.", open, next)
		return Segment{}, next, false
	case !validVariableName(name):
		s.add(CodePatternBadVariableName, fmt.Sprintf(
			"%s is not a variable name. A name uses letters, digits and \"_\", and begins with a letter.",
			quoteForMessage(name)), open+1, nameEnd)
		return Segment{}, next, false
	}
	if !typeOK {
		return Segment{}, next, false
	}
	return Segment{Variable: name, Type: typ, Start: open, End: next}, next, true
}

// regexEnd returns the offset of the parenthesis that closes "regex(", given
// the offset where the expression begins, or -1 when there is none.
//
// It follows the expression the way RE2 reads it, as far as parentheses are
// concerned (PRD 5.2): a backslash escapes the next character; \Q…\E quotes
// everything between; in a character class […] nothing opens or closes a
// group, a "]" right after "[" or "[^" is a member of the class, and a named
// class such as [:alpha:] does not end it. Every other "(" opens a group and
// every ")" closes one.
//
// This decides only where the expression ends. Whether it is a valid
// expression is then asked of Go's regexp, and the two cannot silently
// disagree: if this function stopped at a parenthesis that RE2 does not take
// for the closing one, the text before it would not be balanced, and RE2
// would refuse it.
//
// All characters that matter are ASCII, so the text is read as bytes; a byte
// of a multi-byte character never equals one of them.
func regexEnd(text string, from int) int {
	depth := 1
	for i := from; i < len(text); i++ {
		switch text[i] {
		case '\\':
			if strings.HasPrefix(text[i:], `\Q`) {
				end := strings.Index(text[i+2:], `\E`)
				if end < 0 {
					return -1 // quoted to the end of the text
				}
				i += 2 + end + 1
				continue
			}
			i++ // the escaped character
		case '[':
			i = classEnd(text, i)
			if i < 0 {
				return -1
			}
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// maxClassName is more than the longest name of a class, "^xdigit", and its
// closing ":]".
const maxClassName = 16

// classEnd returns the offset of the "]" that closes the character class
// opened at open, or -1.
func classEnd(text string, open int) int {
	i := open + 1
	if i < len(text) && text[i] == '^' {
		i++
	}
	if i < len(text) && text[i] == ']' {
		i++ // "[]a]" and "[^]a]": a leading "]" is a member
	}
	for ; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '[':
			if strings.HasPrefix(text[i:], "[:") {
				// A named class: [:alpha:], [:^xdigit:]. Its end is looked
				// for only as far as the longest name reaches. RE2 looks
				// further, but takes whatever it then finds for a name and
				// refuses it, so the expression is refused either way; and
				// looking to the end of the text for every "[:" would cost
				// time quadratic in its length.
				window := text[i+2 : min(len(text), i+2+maxClassName)]
				if end := strings.Index(window, ":]"); end >= 0 {
					i += 2 + end + 1
				}
			}
		case ']':
			return i
		}
	}
	return -1
}

// validVariableName reports whether s is a variable name: ASCII letters,
// digits and "_", beginning with a letter (PRD 5.2).
func validVariableName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
		if !letter && (i == 0 || c != '_' && (c < '0' || c > '9')) {
			return false
		}
	}
	return true
}

// byPosition orders problems by where they start, so a form can list them in
// reading order. Problems that start at the same place keep the order in
// which they were found.
func byPosition(problems []Problem) []Problem {
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Start < problems[j].Start })
	return problems
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
