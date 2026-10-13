package linkmodel

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// The fuzz targets of plan section 4: the parser never panics, canonical
// text re-parses to the same pattern, and no value can change a
// destination's scheme or host. FuzzSplitPath lives beside SplitPath's
// tests.

func FuzzParsePattern(f *testing.F) {
	for _, seed := range []string{
		"", "roadmap", "On-Call", "gh/new", "a//b", "/a", "a/", "-a", ".a", "..",
		"café", "a\xffb", "gh/{repo}", "gh/{x:regex(a/b)}", "{", "}", "favicon.ico",
		"a/b/c/d/e/f/g/h/i", strings.Repeat("a", 201), strings.Repeat("é", 150), "a b", "%",
		"reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}", "gh/{repo}/pr/{num:int}", "jira/{key:regex([A-Z]+-[0-9]+)}",
		"x/{a:number}/{b:string}/{c:INT}", "gh/{repo", "gh/{}", "gh/{1a}", "gh/{a}/{a}", "{x}/a", "gh/v{n}", "gh/{n:date}",
		`d/{x:regex(^(a/b|[)}/]|\)|c{1})$)}/e/{y}`, "gh/{n:regex((a)}", `gh/{n:regex(\1)}`, "gh/{n:regex((?=a))}",
		"gh/{n:regex()}", "gh/{n:regex(a)b}", "gh/{a}{b}", "gh/x}", "gh/{n:int}}", `gh/{n:regex(\Q(\E)}`, `gh/{n:regex(\Q)}`,
		"gh/{n:regex([[:alpha:]]+)}", "gh/{n:regex([]a)]+)}", "gh/{n:regex([^])]+)}", "gh/{n:regex(a{1001})}",
		"x/{v:regex((?:" + strings.Repeat("a", 120) + "){1000})}", "x/{a}/{b}/{c}/{d}/{e}/{f}/{g}", "gh/{n:}", "gh/{:int}",
		"gh/{n:regex(\xff)}", "gh/{n:regex(é+)}", "gh/{n:regex(a\\", "gh/{n:regex([", "gh/{n:regex(\\Q", "x/{a:regex(}/{b:regex()}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		p, tokens, problems := scanPattern(pattern, true)
		checkProblems(t, pattern, FieldPattern, problems)
		checkTokens(t, pattern, tokens)
		// What a form reads of a pattern in progress: for a valid pattern the
		// pattern itself; otherwise never more variables than tokens, each
		// one answered by Accepts, and a name only where a name could be.
		name, readable := PatternSoFar(pattern)
		if len(problems) == 0 && (name != p.Name() || readable.String() != p.String()) {
			t.Fatalf("PatternSoFar(%q) = %q, %q; ParsePattern gives %q, %q", pattern, name, readable, p.Name(), p)
		}
		if name != "" && (!PossibleName(name) || ReservedName(name) || name != NormalizeName(name)) {
			t.Fatalf("PatternSoFar(%q) names the link %q", pattern, name)
		}
		if len(readable.Segments()) > len(tokens) || len(readable.Variables()) > MaxPatternVariables+1 {
			t.Fatalf("PatternSoFar(%q) reads %d segments from %d tokens", pattern, len(readable.Segments()), len(tokens))
		}
		for _, v := range readable.Variables() {
			if _, declared := Accepts(readable, Template{}, v, pattern); !declared {
				t.Fatalf("PatternSoFar(%q) lists %q, which Accepts does not know", pattern, v)
			}
		}
		for _, problem := range problems {
			if problem.Severity != SeverityError {
				t.Fatalf("ParsePattern(%q) reported the warning %+v", pattern, problem)
			}
		}
		if again, problemsAgain := ParsePattern(pattern); !samePattern(p, again) || len(problems) != len(problemsAgain) {
			t.Fatalf("ParsePattern(%q) is not deterministic", pattern)
		}

		if len(problems) > 0 {
			if len(p.Segments()) != 0 || p.String() != "" || p.Shape() != "" || p.Name() != "" || p.Variables() != nil {
				t.Fatalf("ParsePattern(%q) returned a pattern with problems: %+v", pattern, p)
			}
			return
		}

		// A pattern without problems obeys every limit.
		segments := p.Segments()
		if len(segments) == 0 || len(segments) > MaxPatternSegments {
			t.Fatalf("ParsePattern(%q) accepted %d segments", pattern, len(segments))
		}
		if len(p.Variables()) > MaxPatternVariables {
			t.Fatalf("ParsePattern(%q) accepted %d variables", pattern, len(p.Variables()))
		}
		if utf8.RuneCountInString(pattern) > MaxPatternLength {
			t.Fatalf("ParsePattern accepted a pattern of %d characters", utf8.RuneCountInString(pattern))
		}
		if !utf8.ValidString(pattern) || !utf8.ValidString(p.String()) {
			t.Fatalf("ParsePattern(%q) accepted text that is not valid UTF-8", pattern)
		}
		if ReservedName(p.Name()) || !PossibleName(p.Name()) {
			t.Fatalf("ParsePattern(%q) accepted the name %q", pattern, p.Name())
		}
		seen := map[string]bool{}
		for _, seg := range segments {
			if !seg.IsVariable() {
				if seg.Literal == "" || !isLiteralStart(rune(seg.Literal[0])) {
					t.Fatalf("ParsePattern(%q) accepted the literal %q", pattern, seg.Literal)
				}
				for _, r := range seg.Literal {
					if !isLiteralRune(r) || 'A' <= r && r <= 'Z' {
						t.Fatalf("ParsePattern(%q) accepted %q in the literal %q", pattern, r, seg.Literal)
					}
				}
				continue
			}
			if !validVariableName(seg.Variable) || seen[NormalizeName(seg.Variable)] {
				t.Fatalf("ParsePattern(%q) accepted the variable name %q", pattern, seg.Variable)
			}
			seen[NormalizeName(seg.Variable)] = true
			if seg.Type.Accepts("") {
				t.Fatalf("ParsePattern(%q): variable %q accepts the empty value", pattern, seg.Variable)
			}
			if !slices.Contains([]string{TypeString, TypeInt, TypeNumber, TypeRegex}, seg.Type.Name()) || seg.Type.Describe() == "" {
				t.Fatalf("ParsePattern(%q): variable %q has type %q (%q)", pattern, seg.Variable, seg.Type.Name(), seg.Type.Describe())
			}
			if seg.Type.Restricted() == (seg.Type.Name() == TypeString) || (seg.Type.Format() == "") != (seg.Type.Name() == TypeString) {
				t.Fatalf("ParsePattern(%q): type %q is restricted %v and written %q", pattern, seg.Type.Name(), seg.Type.Restricted(), seg.Type.Format())
			}
		}
		if len(tokens) != len(segments) {
			t.Fatalf("ParsePattern(%q): %d tokens for %d segments", pattern, len(tokens), len(segments))
		}

		// The canonical text parses to an equal pattern with the same shape
		// (checkRoundTrip), and so does the shape of either text.
		checkRoundTrip(t, pattern, p)
		if Shape(pattern) != p.Shape() || Shape(p.String()) != p.Shape() {
			t.Fatalf("Shape(%q) = %q, want %q for the text and its canonical form", pattern, Shape(pattern), p.Shape())
		}
		// The shape is canonical too: it never depends on a variable's name.
		renamed := pattern
		if len(p.Variables()) > 0 {
			var parts []string
			for i, seg := range segments {
				part := pattern[seg.Start:seg.End]
				if seg.IsVariable() {
					part = "{v" + strconv.Itoa(i) + part[1+len(seg.Variable):]
				}
				parts = append(parts, part)
			}
			renamed = strings.Join(parts, "/")
		}
		if other, otherProblems := ParsePattern(renamed); utf8.RuneCountInString(renamed) <= MaxPatternLength &&
			(len(otherProblems) != 0 || other.Shape() != p.Shape()) {
			t.Fatalf("%q renamed to %q has shape %q (%+v), want %q", pattern, renamed, other.Shape(), otherProblems, p.Shape())
		}

		// A literal-only pattern is a path that resolves to itself.
		if len(p.Variables()) == 0 {
			split, err := SplitPath(pattern)
			if err != nil || !p.fits(split) {
				t.Fatalf("valid pattern %q does not match its own path (split %q, err %v)", pattern, split, err)
			}
		}
		// Whatever parses can be saved and compiled, with a destination that
		// uses every variable, and then never fails to expand.
		dest := "https://example.com/"
		for _, name := range p.Variables() {
			dest += "{" + name + "}/"
		}
		link, draftProblems := ValidateDraft(Draft{Routes: []DraftRoute{{Pattern: pattern, Destination: dest}}})
		if draftProblems.Count() != 0 || link.Name != p.Name() || link.Routes[0].Pattern != p.String() {
			t.Fatalf("ValidateDraft with pattern %q: link %+v, problems %+v", pattern, link, draftProblems)
		}
		compiled := Compile(link)
		if !compiled.routes[0].valid {
			t.Fatalf("the saved pattern %q does not compile", link.Routes[0].Pattern)
		}
		request := make([]string, len(segments))
		for i, seg := range segments {
			request[i] = seg.Literal
			if seg.IsVariable() {
				request[i] = "7"
			}
		}
		if m, ok, _, err := compiled.Match(request); err != nil {
			t.Fatalf("Match(%q) against %q: %v", request, pattern, err)
		} else if ok {
			checkExpanded(t, dest, m.Destination)
		}
	})
}

// checkTokens asserts that tokens lie inside the text, in order and without
// overlap, each on a whole segment.
func checkTokens(t testing.TB, pattern string, tokens []Token) {
	t.Helper()
	previous := 0
	for i, token := range tokens {
		if token.Start < previous || token.End <= token.Start || token.End > len(pattern) {
			t.Fatalf("token %d of %q has range [%d,%d) after offset %d", i, pattern, token.Start, token.End, previous)
		}
		if token.Start > 0 && pattern[token.Start-1] != '/' || token.End < len(pattern) && pattern[token.End] != '/' {
			t.Fatalf("token %d of %q, [%d,%d), is not a whole segment", i, pattern, token.Start, token.End)
		}
		switch token.Kind {
		case TokenLiteral:
			if token.Name != "" || token.Type != "" {
				t.Fatalf("literal token %+v of %q has a name or a type", token, pattern)
			}
		case TokenVariable:
			if !validVariableName(token.Name) || token.Type == "" || pattern[token.Start] != '{' || pattern[token.End-1] != '}' {
				t.Fatalf("variable token %+v of %q", token, pattern)
			}
		default:
			t.Fatalf("token %+v of %q has an unknown kind", token, pattern)
		}
		previous = token.End
	}
}

func FuzzParseDestination(f *testing.F) {
	for _, seed := range []string{
		"", "https://example.com", "http://[::1]:9000/x", "example.com", "ftp://x",
		"javascript:alert(1)", "https://", "https://example.com/{x}", "http://a b/",
		"https://example.com/\r\n", "http://example.com:port/", "\xff", ":", "http:",
		"FTP://example.com/x", "JavaScript:alert(1)", "https://example.com:99999/",
		"https://example.com/\u2028x", "https://example.com/\u202ex", "https://example.com/\u00a0x",
		"https://example.com/żółć?q=ü#frag", "https://user:secret@example.com/",
		"https://example.com@evil.example/", "https://{a}.example.com/", "https://example.com/{a}?b={b}#{a}",
		"https://example.com/{a", "https://example.com/{{a}}", "https://example.com/}", "https://example.com/{}",
		"http://go/x", "http://GO./x", "http://localhost:4655/{a}", `https://example.com\@evil.example/`,
		"{a}://example.com/", "http{a}://example.com", "https://example.com{a}", "https://example.com:{a}/", "https://{a}@example.com/",
		"https://example.com/%7Ba%7D", "https://example.com/%zz", "https:/{a}", "https:{a}", "https://?{a}", "https://#{a}",
		"https://example.com/?q=%{a}", "https://example.com/?q=%4{a}", "https://example.com/?q=%41{a}", "https://example.com/?q=%25{a}{b}",
	} {
		f.Add(seed)
	}
	pattern := mustParse(f, "x/{a}/{b}")
	options := ValidateOptions{OwnHosts: []string{"localhost:4655", "go.example.com"}}

	f.Fuzz(func(t *testing.T, dest string) {
		tmpl, problems := ParseDestination(dest, pattern, options)
		checkProblems(t, dest, FieldDestination, problems)
		// Without a pattern the same text has the same errors, less the ones
		// about which variables exist.
		_, loose := ParseDestination(dest, noPattern, options)
		checkProblems(t, dest, FieldDestination, loose)
		if HasErrors(loose) && !HasErrors(problems) {
			t.Fatalf("%q is refused without a pattern (%+v) and accepted with one", dest, loose)
		}

		if HasErrors(problems) {
			if tmpl.String() != "" || tmpl.HasPlaceholders() {
				t.Fatalf("ParseDestination(%q) returned a template with errors", dest)
			}
			return
		}
		if tmpl.String() != dest {
			t.Fatalf("ParseDestination(%q): the template is %q", dest, tmpl.String())
		}

		// Whatever is accepted is safe to send in a Location header.
		lower := strings.ToLower(dest)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			t.Fatalf("accepted %q, which is not an http or https URL", dest)
		}
		if !utf8.ValidString(dest) {
			t.Fatalf("accepted %q, which is not valid UTF-8", dest)
		}
		for _, r := range dest {
			if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("accepted %q, which contains the invisible or spacing character %U", dest, r)
			}
		}
		if utf8.RuneCountInString(dest) > MaxDestinationLength {
			t.Fatalf("accepted a destination of %d characters", utf8.RuneCountInString(dest))
		}

		// Every brace belongs to a placeholder for a declared variable, and
		// every placeholder stands after the host.
		placeholders := mustPlaceholders(t, dest)
		braces := strings.Count(dest, "{") + strings.Count(dest, "}")
		if braces != 2*len(placeholders) {
			t.Fatalf("accepted %q with %d braces and %d placeholders", dest, braces, len(placeholders))
		}
		authorityEnd := strings.Index(dest, "://") + 3
		if i := strings.IndexAny(dest[authorityEnd:], "/?#"); i >= 0 {
			authorityEnd += i
		} else {
			authorityEnd = len(dest)
		}
		for _, ph := range placeholders {
			if ph.name != "a" && ph.name != "b" {
				t.Fatalf("accepted %q with a placeholder for the undeclared variable %q", dest, ph.name)
			}
			if ph.start < authorityEnd {
				t.Fatalf("accepted %q with a placeholder before the path", dest)
			}
			if before := dest[:ph.start]; strings.HasSuffix(before, "%") ||
				len(before) >= 2 && before[len(before)-2] == '%' && isHexDigit(before[len(before)-1]) {
				t.Fatalf("accepted %q with a placeholder inside a percent escape", dest)
			}
		}
		if strings.ContainsAny(dest[:authorityEnd], `@\{}`) {
			t.Fatalf("accepted %q, whose authority %q holds credentials, a backslash or a brace", dest, dest[:authorityEnd])
		}

		u, err := url.Parse(probe(dest, placeholders))
		if err != nil || u.Hostname() == "" || u.User != nil {
			t.Fatalf("accepted %q, which has no host or has credentials (%v)", dest, err)
		}
		if port := u.Port(); port != "" {
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				t.Fatalf("accepted %q, whose port %q is not a real port", dest, port)
			}
		}
		if host := strings.TrimSuffix(strings.ToLower(u.Hostname()), "."); host == "go" || host == "go.example.com" && u.Port() == "" {
			t.Fatalf("accepted %q, which is a go link or the Directory itself", dest)
		}

		// And it expands, to its own host, whatever the values.
		for _, values := range []map[string]string{
			{"a": "x", "b": "y"},
			{"a": "@evil.example/", "b": "//evil.example\r\n?#"},
			{"a": "..", "b": "%2e%2e/\\"},
		} {
			expanded, err := tmpl.Expand(values)
			if err != nil {
				t.Fatalf("the accepted destination %q does not expand: %v", dest, err)
			}
			checkExpanded(t, dest, expanded)
		}
	})
}

// expandTemplates is the fixed set of valid templates FuzzExpand draws from:
// placeholders in every position a placeholder may have.
var expandTemplates = []string{
	"https://example.com/{a}",
	"https://example.com/{a}/{b}",
	"https://example.com/x/{a}/y/{b}/z",
	"https://example.com/{a}{b}",
	"https://example.com/v{a}.html",
	"https://example.com/?q={a}",
	"https://example.com?q={a}",
	"https://example.com/s?x=1&q={a}&r={b}&y=2",
	"https://example.com/#{a}",
	"https://example.com#{a}",
	"https://example.com/p/{a}?q={b}#{a}",
	"https://example.com/{a}?{b}",
	"https://example.com/{a}#{b}?x/y",
	"http://example.com:8080/{a}",
	"http://[::1]:9000/{a}/{b}",
	"http://127.0.0.1/{a}",
	"HTTPS://Example.COM/Path/{a}",
	"https://example.com/żółć/{a}?ü={b}",
	"https://example.com/%7B{a}%7D/%2F{b}",
	"https://example.com/a/../{a}",
	"https://example.com//{a}//{b}",
	"https://example.com/.{a}",
	"https://example.com/{a}.{b}",
	"https://example.com/{a}:{b}@x",
	"https://example.com/@{a}",
	"https://example.com/;{a}=1",
	"https://a.b-c.example.com./{a}",
	"https://example.com/",
}

// FuzzExpand: for valid templates and arbitrary values, the expansion is a
// URL on the template's scheme and host that the values added no structure
// to (PRD 5.4, NFR-10).
func FuzzExpand(f *testing.F) {
	pattern := mustParse(f, "x/{a}/{b}")
	templates := make([]Template, len(expandTemplates))
	for i, dest := range expandTemplates {
		tmpl, problems := ParseDestination(dest, pattern)
		if HasErrors(problems) {
			f.Fatalf("template %q is not valid: %+v", dest, problems)
		}
		templates[i] = tmpl
	}
	for i := range expandTemplates {
		f.Add(uint8(i), "a b", "c&d")
	}
	for _, seed := range [][2]string{
		{"", ""}, {"..", "."}, {"../..", "..\\.."}, {"a/b", "?x=1"}, {"#frag", "&a=b"}, {"%", "%2F"}, {"%2e%2e", "%00"},
		{"\r\nSet-Cookie: a=b", "\n"}, {" ", "\t"}, {"@evil.example", "//evil.example"}, {"https://evil.example/", ":80"},
		{"żółć", "日本👍"}, {"\xff\xfe", "\x00"}, {"{a}", "}{"}, {"\u202e", "\u2028"}, {"+", "~-._"}, {strings.Repeat("a", 5000), ";"},
	} {
		f.Add(uint8(0), seed[0], seed[1])
		f.Add(uint8(10), seed[0], seed[1])
		f.Add(uint8(22), seed[1], seed[0])
	}

	f.Fuzz(func(t *testing.T, which uint8, a, b string) {
		i := int(which) % len(templates)
		dest, tmpl := expandTemplates[i], templates[i]
		values := map[string]string{"a": a, "b": b}

		expanded, err := tmpl.Expand(values)
		if err != nil {
			t.Fatalf("Expand of %q with %q: %v", dest, values, err)
		}
		// Scheme and host are the template's; no raw CR, LF, space or other
		// forbidden character.
		checkExpanded(t, dest, expanded)

		// No structure was added: the expansion has exactly the delimiters
		// the template has. Compare with the same template expanded with
		// plain letters.
		plain, err := tmpl.Expand(map[string]string{"a": "x", "b": "y"})
		if err != nil {
			t.Fatal(err)
		}
		for _, delimiter := range []string{"/", "?", "#", "&", "=", "@", ":", ";", "\\", "+", ","} {
			if got, want := strings.Count(expanded, delimiter), strings.Count(plain, delimiter); got != want {
				t.Fatalf("Expand of %q with %q = %q: %d of %q, the template has %d", dest, values, expanded, got, delimiter, want)
			}
		}
		// Everything a value contributed is unreserved or a percent escape,
		// so the expansion is the template's text with only such characters
		// in between; and decoding gets the values back unchanged.
		got, want := mustURL(t, expanded), mustURL(t, plain)
		if (got.Fragment == "") != (want.Fragment == "") && !(strings.Contains(dest, "#") && (a == "" || b == "")) {
			t.Fatalf("Expand of %q with %q = %q: the fragment appeared or vanished", dest, values, expanded)
		}
		if len(got.Query()) != len(want.Query()) && a != "" && b != "" {
			t.Fatalf("Expand of %q with %q = %q: %d query parameters, the template has %d",
				dest, values, expanded, len(got.Query()), len(want.Query()))
		}
		if strings.Contains(dest, "?q={a}") {
			if q := got.Query().Get("q"); q != a {
				t.Fatalf("Expand of %q: the q parameter decodes to %q, want the value %q", dest, q, a)
			}
		}
		// Expanding is deterministic and leaves the values alone.
		if again, _ := tmpl.Expand(values); again != expanded || values["a"] != a || values["b"] != b {
			t.Fatalf("Expand of %q is not deterministic or changed its values", dest)
		}
	})
}

func mustURL(t testing.TB, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("%q does not parse: %v", raw, err)
	}
	return u
}

// fuzzLink is the link FuzzMatch resolves against: every kind of segment,
// routes that compete, variables in the path, the query and the fragment,
// and two routes that cannot be read.
func fuzzLink() Link {
	return link("x",
		"x", "https://example.com/",
		"x/new", "https://example.com/new",
		"x/{a}", "https://example.com/{a}",
		"x/{n:int}", "https://example.com/?n={n}",
		"x/{d:number}/n", "https://example.com/#{d}",
		"x/{k:regex([A-Z]+-[0-9]+)}", "https://example.com/k/{k}",
		"x/{a}/pr/{n:int}", "https://example.com/{a}/pull/{n}",
		"x/{a}/{b}/{c}", "https://example.com/{a}?b={b}#{c}",
		"x/{a:regex(.*)}/{b:regex((?s).+)}/{c:regex(\\.+)}/{d}", "https://example.com/{a}{b}/{c}.{d}",
		"x/{", "https://example.com/",
		"x/{v}/broken", "https://{v}.example.com/",
	)
}

// dotStep is the rule of NFR-10 written a second time, as an expression, for
// the fuzz targets to hold hasDotStep against: "." or ".." alone or between
// slashes of either kind.
var dotStep = regexp.MustCompile(`(?s)(^|[/\\])\.\.?([/\\]|$)`)

// FuzzMatch: matching never panics, a hit is consistent with its route, and
// a miss explains every route.
func FuzzMatch(f *testing.F) {
	for _, seed := range []string{
		"", "x", "X", "x/new", "x/NEW", "x/abc", "x/42", "x/-7", "x/3.5/n", "x/OPS-12", "x/ops-12", "x/a/pr/7", "x/a/pr/x",
		"x/a/b/c", "x/a/b/c/d", "x/./../...", "x/..", "x/.", "x/a/b/../d", "x/a/b/c/d/e/f/g/h/i", "y", "x//", "/x/",
		"x/\xff", "x/\x00", "x/a b", "x/%2F", "x/\r\n", "x/żółć", "x/{a}", "x/@evil.example", "x/a/\n/./d",
		`x/..\y`, `x/a\..`, `x/.\`, `x/a\.\b/pr/7`, `x/..\../b/c`,
	} {
		f.Add(seed)
	}
	l := fuzzLink()
	c := Compile(l)
	codes := []string{ReasonSegmentCount, ReasonLiteralMismatch, ReasonValueRejected, ReasonValueNotAllowed, ReasonRouteInvalid}

	f.Fuzz(func(t *testing.T, path string) {
		// The segments are arbitrary: split without decoding, so that they
		// may be empty, hold invalid UTF-8, or be anything else that
		// SplitPath would never produce.
		segments := strings.Split(path, "/")
		// The whole path as one value: the two readings of the dot rule
		// agree on text with slashes of both kinds.
		if got, want := hasDotStep(path), dotStep.MatchString(path); got != want {
			t.Fatalf("hasDotStep(%q) = %v, and the expression says %v", path, got, want)
		}
		m, ok, reasons, err := c.Match(segments)
		if err != nil {
			t.Fatalf("Match(%q): %v", segments, err)
		}

		// An independent reading of the rule: a route answers when its
		// pattern fits and no value bound for the path is a dot segment or
		// has one between slashes (dotStep).
		answers := func(i int) bool {
			r := c.routes[i]
			if !r.valid || !r.pattern.fits(segments) {
				return false
			}
			for j, seg := range r.pattern.Segments() {
				if seg.IsVariable() && r.template.InPath(seg.Variable) && dotStep.MatchString(segments[j]) {
					return false
				}
			}
			return true
		}
		any := false
		for i := range l.Routes {
			any = any || answers(i)
		}
		if ok != any {
			t.Fatalf("Match(%q) ok = %v, but a route answering it exists = %v", segments, ok, any)
		}

		if !ok {
			if len(reasons) != len(l.Routes) || m.Destination != "" || m.Values != nil || m.Route.ID != 0 {
				t.Fatalf("Match(%q) missed with %d reasons and match %+v", segments, len(reasons), m)
			}
			for i, r := range reasons {
				if r.RouteID != l.Routes[i].ID || r.Pattern != l.Routes[i].Pattern || !slices.Contains(codes, r.Code) {
					t.Fatalf("Match(%q) reason %d = %+v", segments, i, r)
				}
				if r.Message == "" || !utf8.ValidString(r.Message) || !strings.HasSuffix(r.Message, ".") ||
					strings.ContainsAny(r.Message, "\r\n\x00") || len(r.Message) > 700 {
					t.Fatalf("Match(%q) reason %d has the message %q", segments, i, r.Message)
				}
				if (r.Code == ReasonValueRejected || r.Code == ReasonValueNotAllowed) != (r.Variable != "") {
					t.Fatalf("Match(%q) reason %d = %+v: a variable is named exactly for the value codes", segments, i, r)
				}
				if r.Value != "" && !slices.Contains(segments, r.Value) {
					t.Fatalf("Match(%q) reason %d names the value %q, which the request does not have", segments, i, r.Value)
				}
			}
			return
		}

		if reasons != nil {
			t.Fatalf("Match(%q) hit and returned reasons", segments)
		}
		index := int(m.Route.ID) - 1
		if index < 0 || index >= len(l.Routes) || !answers(index) {
			t.Fatalf("Match(%q) chose route %d, which does not answer it", segments, m.Route.ID)
		}
		// No route that answers is more specific, or equally specific and
		// listed earlier.
		for i := range l.Routes {
			if i != index && answers(i) && c.moreSpecific(i, index) {
				t.Fatalf("Match(%q) chose route %d over the more specific route %d", segments, index+1, i+1)
			}
		}
		route := c.routes[index]
		if len(m.Values) != len(route.pattern.Variables()) {
			t.Fatalf("Match(%q) values = %q for the variables %q", segments, m.Values, route.pattern.Variables())
		}
		for j, seg := range route.pattern.Segments() {
			if seg.IsVariable() && m.Values[seg.Variable] != segments[j] {
				t.Fatalf("Match(%q) value of %q = %q, want the segment unchanged", segments, seg.Variable, m.Values[seg.Variable])
			}
		}
		checkExpanded(t, l.Routes[index].Destination, m.Destination)
	})
}
