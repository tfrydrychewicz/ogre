package linkmodel

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits on a regular expression type.
//
// Go's regexp is an RE2 engine: the time one match takes is linear in the
// value, whatever the expression. Linear is not the same as small, though.
// The factor is the size of the compiled program, and a request can make
// every regular expression of a link run (six variables in each of twenty
// routes). NFR-8 asks that no expression can stall the resolver, so three
// limits together bound what one request can cost:
//
//	work per request <= MaxLinkRegexProgram x MaxRegexValueLength
//
// in steps of the engine, each of them about ten nanoseconds: some ten
// milliseconds for the worst link that can be saved, where it used to be
// seconds. TestWorstRegexLinkIsBounded holds the bound.
const (
	// MaxRegexLength is the longest expression accepted, in characters
	// (PRD 5.3).
	MaxRegexLength = 200
	// MaxRegexProgram is the largest compiled program accepted for one
	// expression when a pattern is saved, in instructions of Go's regexp
	// engine. A repeat count makes a short expression large: "(a…a){1000}"
	// in 135 characters compiles to 128,000 instructions and several
	// megabytes. Ordinary expressions are far below the cap: the month
	// expression of PRD 5.7 has 14 instructions, "[0-9a-f]{64}" about 70,
	// ".{1,200}" about 400.
	MaxRegexProgram = 1000
	// MaxLinkRegexProgram is the largest sum of the programs of all regular
	// expressions in all routes of one link, checked when the link is saved
	// (ValidateDraft). Without it a link could hold 120 expressions of
	// MaxRegexProgram each, and one request would run them all.
	MaxLinkRegexProgram = 4000
	// MaxRegexValueLength is the longest value, in bytes, that a regular
	// expression is run on; a longer value is not accepted. A value that a
	// regular expression is meant to restrict is a number, a code, a date or
	// a name: short. Text of any length belongs in a plain {variable}, which
	// costs nothing to match.
	MaxRegexValueLength = 256
)

// The names of the variable types (VarType.Name).
const (
	TypeString = "string"
	TypeInt    = "int"
	TypeNumber = "number"
	TypeRegex  = "regex"
)

// TypeChoice is a variable type as a form offers it: its name in a pattern
// and what people call it.
type TypeChoice struct {
	Name, Label string
}

// TypeChoices are the types a form offers for a variable, in the order it
// offers them, with the words every page uses for them: the create form's
// type selector and the link page's table show the same label. A type added
// here (L6) is offered and shown everywhere at once.
var TypeChoices = []TypeChoice{
	{TypeString, "Text"},
	{TypeInt, "Whole number"},
	{TypeNumber, "Number"},
	{TypeRegex, "Regular expression"},
}

// TypeLabel is what people call the type with the given name (TypeChoices);
// the name itself for a type that is not offered.
func TypeLabel(name string) string {
	for _, c := range TypeChoices {
		if c.Name == name {
			return c.Label
		}
	}
	return name
}

// typeList is how messages tell a user which types exist.
const typeList = "string, int, number and regex(…)"

// VarType is the type of a variable: the rule for which values it accepts
// (PRD 5.3). The link model implements it for string, int, number and regex;
// later milestones add types through the same interface, which is why
// matching, shapes and messages ask the type instead of switching on a name.
//
// A VarType is immutable and safe for concurrent use.
type VarType interface {
	// Name is the type's keyword: "string", "int", "number" or "regex".
	Name() string
	// Accepts reports whether the type accepts value, the decoded text of
	// one request segment. The value is compared exactly as it was typed:
	// nothing is trimmed, folded or reformatted. The empty value is never
	// accepted, by any type.
	Accepts(value string) bool
	// Describe says what the type accepts, to finish the sentence "It
	// accepts …": "any text", "a whole number".
	Describe() string
	// Restricted reports whether the type narrows what a segment may be. A
	// restricted variable is more specific than a text variable when two
	// routes match the same request (PRD 5.5, step 4).
	Restricted() bool
	// Format is the canonical text that follows the colon in a pattern:
	// "int", "regex(…)". It is "" for string, which is written without a
	// type.
	Format() string
}

// StringType returns the default type, which accepts any non-empty text.
func StringType() VarType { return stringType{} }

// IntType returns the integer type.
func IntType() VarType { return intType{} }

// NumberType returns the number type.
func NumberType() VarType { return numberType{} }

type stringType struct{}

func (stringType) Name() string          { return TypeString }
func (stringType) Accepts(v string) bool { return v != "" }
func (stringType) Describe() string      { return "any text" }
func (stringType) Restricted() bool      { return false }
func (stringType) Format() string        { return "" }

// intType accepts digits with an optional leading "-" (PRD 5.3).
//
// It is a rule about text, not about a machine integer: the value is passed
// on as typed and never converted, so leading zeros ("03"), "-0" and a digit
// string of any length are accepted, and there is no overflow to guard
// against. A "+" sign, digits outside ASCII and a lone "-" are not.
type intType struct{}

func (intType) Name() string     { return TypeInt }
func (intType) Describe() string { return "a whole number" }
func (intType) Restricted() bool { return true }
func (intType) Format() string   { return TypeInt }

func (intType) Accepts(v string) bool {
	digits := strings.TrimPrefix(v, "-")
	return digits != "" && digitRun(digits) == len(digits)
}

// numberType accepts "an integer or a decimal such as 3.5" (PRD 5.3): what
// int accepts, optionally followed by "." and at least one digit.
//
// The wording is read strictly, so that a value means the same to every
// destination it is passed to: "3." and ".5" have no digits on one side of
// the point and are refused, and so are exponents ("1e3"), a "+" sign, a
// thousands separator and a decimal comma.
type numberType struct{}

func (numberType) Name() string     { return TypeNumber }
func (numberType) Describe() string { return "a number such as 42 or 3.5" }
func (numberType) Restricted() bool { return true }
func (numberType) Format() string   { return TypeNumber }

func (numberType) Accepts(v string) bool {
	v = strings.TrimPrefix(v, "-")
	whole := digitRun(v)
	if whole == 0 {
		return false
	}
	if whole == len(v) {
		return true
	}
	fraction := v[whole+1:]
	return v[whole] == '.' && fraction != "" && digitRun(fraction) == len(fraction)
}

// digitRun returns how many ASCII digits s begins with. Only ASCII counts:
// "٣" and "３" are digits to Unicode and to nothing a destination expects.
func digitRun(s string) int {
	n := 0
	for n < len(s) && '0' <= s[n] && s[n] <= '9' {
		n++
	}
	return n
}

// regexType accepts a value the expression matches from start to end.
type regexType struct {
	source string
	// size is the number of instructions of the compiled program, measured
	// only for a pattern that is being saved (see newRegexType); 0 otherwise.
	size int
	// re is the source wrapped as ^(?:…)$, compiled once when the pattern is
	// parsed. Go's regexp is an RE2 implementation: matching takes time
	// linear in the value whatever the expression (NFR-8). A *regexp.Regexp
	// is safe for concurrent use.
	re *regexp.Regexp
}

func (regexType) Name() string       { return TypeRegex }
func (regexType) Restricted() bool   { return true }
func (t regexType) Describe() string { return "text matching " + t.source }
func (t regexType) Format() string   { return "regex(" + t.source + ")" }

func (t regexType) Accepts(v string) bool {
	// An empty value never matches, even when the expression would match
	// the empty string (PRD 5.3).
	return v != "" && len(v) <= MaxRegexValueLength && t.re.MatchString(v)
}

// errRegex is a refusal of a regular expression: a problem code and the
// sentence that explains it.
type errRegex struct {
	code, message string
}

func (e *errRegex) Error() string { return e.message }

// RegexProblem checks a regular expression on its own, as it is typed into
// a field of its own (the form's variables table, VAR-3), by the rules a
// regex variable of a pattern being saved follows. It returns the code and
// sentence of the first problem, in the terms of the expression alone, or
// two empty strings when regex(source) would be accepted.
//
// Inside a pattern, an expression that opens a "(" or a "[" and never closes
// it hides where regex( ends, and the pattern can only say that the closing
// parenthesis is missing. Asked on its own, the same expression gets RE2's
// own complaint ("a character class is opened with [ and never closed with
// ]"), which is what the author has to fix.
func RegexProblem(source string) (code, message string) {
	if _, bad := newRegexType(source, true); bad != nil {
		return bad.code, bad.message
	}
	// RE2 accepts it; so would a pattern, unless it cannot tell where the
	// expression ends. That is only possible with \Q and no \E: RE2 quotes
	// to the end of the expression, a pattern to the end of the pattern.
	if regexEnd(source+")", 0) != len(source) {
		return CodePatternRegexUnbalanced,
			"This expression can't be placed in a pattern as it is: end every \\Q with \\E."
	}
	return "", ""
}

// newRegexType compiles the expression of a regex(…) type.
//
// The expression must match the whole value whether or not it is written
// with "^" and "$" (PRD 5.3), so it is compiled inside ^(?:…)$. Flags set in
// the expression, such as (?i), stay inside that group and cannot reach the
// anchors.
//
// The bare source is checked first and on its own: an error then speaks of
// the text the user wrote, and a source that is only valid together with the
// wrapper cannot slip through.
//
// checkSize is true for a pattern that is being saved, and adds the rules
// that only bind such a pattern: no raw control characters, and a program of
// at most MaxRegexProgram instructions. It is false for a pattern read back
// from the store. The size of a compiled program is a detail of the Go
// release; a route that was saved under one release must not stop matching
// under the next.
func newRegexType(source string, checkSize bool) (VarType, *errRegex) {
	if source == "" {
		return nil, &errRegex{CodePatternRegexEmpty,
			"Write an expression between the parentheses, such as regex([0-9]+)."}
	}
	if n := utf8.RuneCountInString(source); n > MaxRegexLength {
		return nil, &errRegex{CodePatternRegexTooLong, fmt.Sprintf(
			"A regular expression can have at most %d characters; this one has %d.", MaxRegexLength, n)}
	}
	if _, err := syntax.Parse(source, syntax.Perl); err != nil {
		return nil, &errRegex{CodePatternRegexInvalid, regexMessage(source, err)}
	}
	wrapped := "^(?:" + source + ")$"
	size := 0
	if checkSize {
		// A pattern is shown wherever its link is: in usage help, in a miss
		// page, in JSON. A raw line break or other control character inside
		// it would break those lines apart, and is never needed: the
		// expression can name the character instead.
		for _, r := range source {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				return nil, &errRegex{CodePatternBadCharacter, fmt.Sprintf(
					"A regular expression can't contain a line break or another control character as it is. Write it as an escape, such as \\n or \\x{%X}.", r)}
			}
		}
		// Measure what will run: the wrapped expression, simplified the way
		// regexp.Compile simplifies it.
		whole, err := syntax.Parse(wrapped, syntax.Perl)
		if err == nil {
			var prog *syntax.Prog
			if prog, err = syntax.Compile(whole.Simplify()); err == nil {
				if size = len(prog.Inst); size > MaxRegexProgram {
					return nil, &errRegex{CodePatternRegexTooComplex,
						"This regular expression is too complex, usually because of a large repeat count such as {500}. Use a simpler one."}
				}
			}
		}
		if err != nil {
			return nil, &errRegex{CodePatternRegexInvalid, regexMessage(source, err)}
		}
	}
	re, err := compileShared(wrapped)
	if err != nil {
		return nil, &errRegex{CodePatternRegexInvalid, regexMessage(source, err)}
	}
	return regexType{source: source, size: size, re: re}, nil
}

// regexProgram returns the sum of the program sizes of the pattern's regular
// expressions. It is meaningful for a pattern parsed for saving
// (ParsePattern); for one read back from the store it is 0.
func regexProgram(p Pattern) int {
	total := 0
	for _, seg := range p.segments {
		if t, ok := seg.Type.(regexType); ok {
			total += t.size
		}
	}
	return total
}

// regexMessage turns a compile error of Go's regexp into a sentence for
// someone who wrote the expression by hand. The engine's own text ("invalid
// or unsupported Perl syntax: `(?=`") names the rule it broke, not what to
// do; the two refusals that matter most, backreferences and lookaround, are
// features of other engines that RE2 leaves out on purpose, and the message
// says so.
func regexMessage(source string, err error) string {
	const lead = "The regular expression is not valid: "
	var se *syntax.Error
	if !errors.As(err, &se) {
		return lead + "it can't be read."
	}
	near := se.Expr
	// An error found while compiling the wrapped form may quote the wrapper;
	// only repeat text the user wrote.
	if !strings.Contains(source, near) || !utf8.ValidString(near) {
		near = ""
	}
	quoted := func(format string) string {
		if near == "" {
			return lead + "it can't be read."
		}
		return lead + fmt.Sprintf(format, near)
	}
	lookaround := func() bool {
		for _, op := range []string{"(?=", "(?!", "(?<=", "(?<!"} {
			if strings.HasPrefix(se.Expr, op) {
				return true
			}
		}
		return false
	}
	switch se.Code {
	case syntax.ErrInvalidPerlOp, syntax.ErrInvalidNamedCapture:
		if lookaround() {
			return lead + "lookahead and lookbehind, such as (?=…) and (?<!…), are not supported."
		}
		if se.Code == syntax.ErrInvalidNamedCapture {
			return quoted("%q is not a valid named group. Write it as (?P<name>…).")
		}
		return quoted("%q is not supported.")
	case syntax.ErrInvalidEscape:
		if len(near) == 2 && near[0] == '\\' && '1' <= near[1] && near[1] <= '9' {
			return lead + fmt.Sprintf("backreferences such as %s are not supported.", near)
		}
		return quoted("%q is not a valid escape.")
	case syntax.ErrTrailingBackslash:
		return lead + "it ends with a backslash that escapes nothing."
	case syntax.ErrMissingParen:
		return lead + "a parenthesis is opened and never closed."
	case syntax.ErrUnexpectedParen:
		return lead + "a parenthesis is closed that was never opened."
	case syntax.ErrMissingBracket:
		return lead + "a character class is opened with [ and never closed with ]."
	case syntax.ErrInvalidCharRange:
		return quoted("%q is not a valid range in a character class.")
	case syntax.ErrInvalidCharClass:
		return quoted("%q is not a valid character class.")
	case syntax.ErrMissingRepeatArgument:
		return quoted("%q has nothing before it to repeat.")
	case syntax.ErrInvalidRepeatOp:
		return quoted("%q repeats a repetition. Use one of *, + and ? at a time.")
	case syntax.ErrInvalidRepeatSize:
		return quoted("the repeat count %q is too large. The largest is 1000, nested counts multiplied.")
	case syntax.ErrInvalidUTF8:
		return lead + "it contains text that is not valid UTF-8."
	case syntax.ErrLarge, syntax.ErrNestingDepth:
		return lead + "it is too large or nested too deeply."
	}
	return lead + "it can't be read."
}

// parseType turns the text after a variable's colon into a type. text is
// never a regex(…) with its closing parenthesis found: the tokenizer handles
// that form itself, because only it knows where the expression ends.
func parseType(text string) (VarType, string) {
	switch NormalizeName(text) {
	case TypeString:
		return stringType{}, ""
	case TypeInt:
		return intType{}, ""
	case TypeNumber:
		return numberType{}, ""
	case TypeRegex:
		return nil, "Write the expression in parentheses after regex, as in {id:regex([0-9]+)}."
	case "":
		return nil, "Write a type after the colon, or remove the colon. The types are " + typeList + "."
	}
	return nil, fmt.Sprintf("%s is not a known type. The types are %s.", quoteForMessage(text), typeList)
}
