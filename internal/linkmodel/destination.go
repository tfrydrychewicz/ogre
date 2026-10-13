package linkmodel

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDestinationLength is the longest destination accepted, in characters
// (PRD 5.4). Browsers and proxies handle URLs of this size reliably.
const MaxDestinationLength = 2048

// Bounds on the work of checking text that is not a destination, like those
// for a pattern (maxPatternScan, maxPatternProblems).
const (
	maxDestinationScan     = 4 * MaxDestinationLength // bytes
	maxDestinationProblems = 20
)

// ErrExpand is returned (wrapped) by Template.Expand when the expanded URL
// is not on the template's scheme and host, or the template cannot be
// expanded at all. It always means a bug or damaged data, never a bad
// request: the caller answers 500 and does not redirect.
var ErrExpand = errors.New("destination template did not expand safely")

// ValidateOptions is what the save rules need to know about the Directory
// they run in. The link model is pure and has no configuration of its own;
// the application passes it in.
type ValidateOptions struct {
	// OwnHosts are the hosts under which the Directory itself is reached. A
	// destination on one of them is refused, because a go link that leads
	// back into the Directory can loop (PRD 5.4).
	//
	// An entry is a host with an optional port ("localhost:4655",
	// "go.example.com", "[::1]:4655") or a base URL
	// ("https://go.example.com"). An entry without a port stands for the
	// default port of its scheme, or of the destination's scheme when the
	// entry has none. Hosts are compared ignoring case. A different port on
	// the same host is a different site and is allowed: on a developer's
	// machine most destinations are other ports of localhost.
	//
	// An address is recognised in every spelling a browser accepts for it:
	// "127.1:4655", "2130706433:4655" and "localhost:04655" are the
	// Directory when "127.0.0.1:4655" and "localhost:4655" are (see
	// canonicalHost).
	OwnHosts []string
}

// Where a placeholder sits in a destination.
const (
	inPath = iota
	inQuery
	inFragment
)

// templatePart is one piece of a destination: text copied as it is, or a
// variable whose value is inserted percent-encoded.
type templatePart struct {
	literal  string
	variable string // "" for a literal
}

// Template is a destination after parsing: the text cut at its placeholders,
// with the scheme and host that every expansion must keep. The zero Template
// expands to an error. A Template is immutable.
type Template struct {
	text  string
	parts []templatePart
	// scheme and host are what url.Parse reports for the template, kept for
	// the assertion in Expand. They are empty when the template has no
	// placeholders: nothing is inserted, so nothing can change.
	scheme, host string
	// pathVariables are the variables with a placeholder in the path.
	pathVariables []string
}

// String returns the destination as it was written.
func (t Template) String() string { return t.text }

// HasPlaceholders reports whether the destination inserts any variable.
func (t Template) HasPlaceholders() bool { return len(t.parts) > 1 }

// InPath reports whether a placeholder of the variable sits in the
// destination's path, as opposed to its query or fragment. Such a variable
// must not take the values "." and "..": a browser resolves those as path
// steps, so a request could climb out of the path the owner wrote (NFR-10).
func (t Template) InPath(variable string) bool {
	for _, v := range t.pathVariables {
		if v == variable {
			return true
		}
	}
	return false
}

// Expand substitutes the values into the template and returns the URL to
// redirect to (PRD 5.4).
//
// A value is inserted percent-encoded, byte by byte, leaving only the
// unreserved characters A-Z a-z 0-9 "-" "." "_" "~" as they are. Whatever a
// request carried, it therefore arrives as data inside the one path segment,
// query value or fragment where the placeholder stands: it cannot add a
// segment, a parameter, a fragment or a line break.
//
// That property already follows from the encoding and from where a
// placeholder may stand. Expand asserts it all the same: the result must
// parse as a URL with the template's scheme and host, or Expand returns an
// error wrapping ErrExpand and no URL. A redirect to a host the owner did
// not write is the one mistake this package must never make, so it does not
// rest on a single argument.
//
// A variable of the template that is missing from values is an error too.
// The error text never contains a value (NFR-17).
func (t Template) Expand(values map[string]string) (string, error) {
	switch len(t.parts) {
	case 0:
		return "", fmt.Errorf("%w: the template is empty", ErrExpand)
	case 1:
		if t.parts[0].variable == "" {
			return t.text, nil // no placeholders: the destination as written
		}
	}
	var b strings.Builder
	b.Grow(len(t.text) + 16)
	for _, part := range t.parts {
		if part.variable == "" {
			b.WriteString(part.literal)
			continue
		}
		value, ok := values[part.variable]
		if !ok {
			return "", fmt.Errorf("%w: no value for variable %q", ErrExpand, part.variable)
		}
		writeEncoded(&b, value)
	}
	expanded := b.String()
	u, err := url.Parse(expanded)
	if err != nil {
		return "", fmt.Errorf("%w: the expanded URL does not parse", ErrExpand)
	}
	if u.Scheme != t.scheme || u.Host != t.host {
		return "", fmt.Errorf("%w: the expanded URL is not on %s://%s", ErrExpand, t.scheme, t.host)
	}
	return expanded, nil
}

// writeEncoded appends v percent-encoded, keeping only unreserved characters
// (RFC 3986, section 2.3). url.PathEscape and url.QueryEscape both keep more
// ("$&+,:;=@" in a path, and a space as "+" in a query), which is why
// neither is used: one rule for every position is easier to trust.
func writeEncoded(b *strings.Builder, v string) {
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(v); i++ {
		c := v[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
}

// ParseDestination checks where a route redirects to and returns it parsed
// (PRD 5.4). p is the route's pattern; pass the zero Pattern when the
// pattern itself did not parse, and the placeholders are then checked for
// their form only.
//
// The rules:
//
//   - An absolute http or https URL with a host, of at most
//     MaxDestinationLength characters. Restricting the scheme is a safety
//     rule (NFR-9): the destination is sent in a Location header, and a
//     "javascript:" or "file:" destination would turn a go link into a way to
//     run script or open local files.
//   - No whitespace, control characters or invisible formatting characters,
//     in all of Unicode (see firstBadCharacter). A URL never needs them
//     unencoded, and a line break in a header value is how response splitting
//     starts. Other non-ASCII text is allowed and kept as written.
//   - No credentials: nothing before an "@" in front of the host. Otherwise
//     "https://example.com@evil.example/" would read as a link to
//     example.com and open evil.example.
//   - {name} is a placeholder. It must name a variable of the pattern, and
//     may stand in the path, the query or the fragment, never before the
//     path and never as the end of a percent escape ("%{x}"). A literal
//     brace is written %7B or %7D.
//   - The host is not "go" (that would be a go link) and the URL is not on
//     the Directory itself (opts.OwnHosts); both could loop.
//
// A variable of the pattern that the destination does not use is reported as
// a warning.
//
// Where a placeholder stands is decided on the text, before any URL parser
// sees it: the authority is what lies between "://" and the next "/", "?" or
// "#". A browser ends the authority at a "\" as well, so it never sees a
// longer authority than this function does, and a placeholder that is past
// the authority here is past it there.
//
// It returns the template and any warnings when the destination can be
// saved; otherwise the zero Template and the problems. Problems are ordered
// by position, each with the byte range it concerns.
func ParseDestination(dest string, p Pattern, opts ...ValidateOptions) (Template, []Problem) {
	var o ValidateOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	t, problems := parseDestination(dest, p, o)
	if HasErrors(problems) {
		return Template{}, problems
	}
	return t, problems
}

// placeholder is one {name} of a destination and where it is.
type placeholder struct {
	name       string
	start, end int
	where      int // inPath, inQuery or inFragment
}

func parseDestination(dest string, p Pattern, o ValidateOptions) (Template, []Problem) {
	var problems []Problem
	report := func(severity, code, message string, start, end int) {
		if len(problems) >= maxDestinationProblems {
			return // already refused, and with enough to act on
		}
		problems = append(problems, Problem{
			Severity: severity, Field: FieldDestination,
			Code: code, Message: message, Start: start, End: end,
		})
	}
	add := func(code, message string, start, end int) { report(SeverityError, code, message, start, end) }

	if dest == "" {
		add(CodeDestinationEmpty, "Enter the URL this link should open.", 0, 0)
		return Template{}, problems
	}
	if over := offsetOfRune(dest, MaxDestinationLength); over >= 0 {
		add(CodeDestinationTooLong, fmt.Sprintf(
			"A destination can have at most %d characters; this one has %d.",
			MaxDestinationLength, utf8.RuneCountInString(dest)), over, len(dest))
	}
	if len(dest) > maxDestinationScan {
		// Far beyond the limit: say so and read no further.
		return Template{}, problems
	}
	if start, end, found := firstBadCharacter(dest); found {
		add(CodeDestinationBadCharacter,
			"A destination can't contain spaces, line breaks, control characters or invisible characters. "+
				"Write a space as %20.", start, end)
		return Template{}, byPosition(problems) // nothing after this can be parsed reliably
	}

	placeholders, bracesOK := scanPlaceholders(dest, add)

	// The structure of the URL, read from the text.
	var (
		scheme, hasScheme = leadingScheme(dest)
		web               = hasScheme && (strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https"))
		authorityFrom     = -1 // offset of the authority; -1 when there is none
		authorityTo       = 0  // offset where the path, query or fragment begins
	)
	switch {
	case !hasScheme:
		add(CodeDestinationNotAbsolute,
			"A destination must be a full URL that starts with http:// or https://.", 0, len(dest))
		// Still say so when a placeholder stands where the scheme or the
		// host would be: "{env}://example.com", "http{s}://{host}/".
		if i := strings.Index(dest, "://"); i >= 0 {
			authorityFrom = i + 3
		}
	case !web:
		add(CodeDestinationScheme, fmt.Sprintf(
			"A destination must start with http:// or https://, not %q.", scheme+":"), 0, len(scheme))
		if strings.HasPrefix(dest[len(scheme):], "://") {
			authorityFrom = len(scheme) + 3
		}
	case strings.HasPrefix(dest[len(scheme):], "://"):
		authorityFrom = len(scheme) + 3
	}
	if authorityFrom >= 0 {
		authorityTo = len(dest)
		if i := strings.IndexAny(dest[authorityFrom:], "/?#"); i >= 0 {
			authorityTo = authorityFrom + i
		}
	}

	beforePath := false
	for _, ph := range placeholders {
		if ph.start < authorityTo {
			beforePath = true
			add(CodeDestinationPlaceholderInHost, fmt.Sprintf(
				"{%s} can't be used here. A variable may appear in the path, the query or the fragment, "+
					"never in the scheme, the host or the port, so a request can't change which site the link opens.",
				ph.name), ph.start, ph.end)
		}
	}
	credentials := false
	if web && authorityFrom >= 0 {
		authority := dest[authorityFrom:authorityTo]
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			credentials = true
			add(CodeDestinationCredentials,
				"A destination can't contain a user name or password before the host. "+
					"Remove everything up to and including the @.", authorityFrom, authorityFrom+at+1)
		}
	}

	// A placeholder may not finish a percent escape that the template
	// begins ("%{hex}", "%4{digit}"): the value would then choose which
	// character the escape stands for, instead of being data of its own.
	inEscape := false
	for _, ph := range placeholders {
		from := ph.start
		switch {
		case from >= 1 && dest[from-1] == '%':
			from--
		case from >= 2 && dest[from-2] == '%' && isHexDigit(dest[from-1]):
			from -= 2
		default:
			continue
		}
		inEscape = true
		add(CodeDestinationInvalid, fmt.Sprintf(
			"{%s} can't follow a %% directly, because the two would read as one escaped character. Write a literal %% as %%25.",
			ph.name), from, ph.end)
	}

	var parsed *url.URL
	switch {
	case !web:
	case !bracesOK || beforePath || inEscape:
		// A stray brace can make the URL unparseable; the problem already
		// reported says what to fix.
	default:
		u, err := url.Parse(probe(dest, placeholders))
		switch {
		case err != nil && credentials:
			// The credentials are what is wrong with it.
		case err != nil:
			add(CodeDestinationInvalid,
				"This is not a valid URL. Check the host, the port and any % escapes.", 0, len(dest))
		case u.Hostname() == "":
			add(CodeDestinationNoHost,
				"The destination has no host. Write it like https://example.com/page.", 0, len(dest))
		case !validPort(u.Port()):
			add(CodeDestinationInvalid,
				"This is not a valid URL. Check the host, the port and any % escapes.", 0, len(dest))
		default:
			parsed = u
			if u.User != nil && !credentials {
				credentials = true
				add(CodeDestinationCredentials,
					"A destination can't contain a user name or password before the host.", 0, len(dest))
			}
		}
	}
	if parsed != nil && !credentials {
		switch host := bareHost(parsed); {
		case host == "go":
			add(CodeDestinationGoLink,
				"A destination can't be another go link, because that could loop. Enter the page the link should open.",
				authorityFrom, authorityTo)
		case ownHost(parsed, o.OwnHosts):
			add(CodeDestinationDirectory,
				"A destination can't be a page of this go link directory itself, because that could loop.",
				authorityFrom, authorityTo)
		}
	}

	// Placeholders against the pattern's variables. The zero Pattern means
	// the pattern did not parse; what it declares is then unknown, and
	// guessing would bury the real problem under false ones.
	if len(p.segments) > 0 {
		used := map[string]bool{}
		for _, ph := range placeholders {
			used[ph.name] = true
			if _, declared := p.variable(ph.name); !declared {
				add(CodeDestinationUndeclaredVariable, undeclaredMessage(ph.name, p), ph.start, ph.end)
			}
		}
		for _, name := range p.variables {
			// Only for a destination that is otherwise fine: next to an
			// error, "you don't use {x}" is noise.
			if !used[name] && !HasErrors(problems) {
				report(SeverityWarning, CodeDestinationUnusedVariable, fmt.Sprintf(
					"The destination doesn't use {%s}. The link works, but that part of the request is ignored.",
					name), len(dest), len(dest))
			}
		}
	}

	problems = byPosition(problems)
	if HasErrors(problems) || parsed == nil {
		return Template{}, problems
	}
	return newTemplate(dest, placeholders, parsed), problems
}

// newTemplate cuts dest at its placeholders. parsed is the URL of the probe,
// which has the template's scheme and host.
func newTemplate(dest string, placeholders []placeholder, parsed *url.URL) Template {
	t := Template{text: dest}
	if len(placeholders) == 0 {
		t.parts = []templatePart{{literal: dest}}
		return t
	}
	t.scheme, t.host = parsed.Scheme, parsed.Host
	at := 0
	for _, ph := range placeholders {
		if ph.start > at {
			t.parts = append(t.parts, templatePart{literal: dest[at:ph.start]})
		}
		t.parts = append(t.parts, templatePart{variable: ph.name})
		if ph.where == inPath && !t.InPath(ph.name) {
			t.pathVariables = append(t.pathVariables, ph.name)
		}
		at = ph.end
	}
	if at < len(dest) {
		t.parts = append(t.parts, templatePart{literal: dest[at:]})
	}
	if len(t.parts) == 1 {
		// The whole destination is one placeholder. Validation never lets
		// that through, but Expand tells a static template by its single
		// part, so keep the two apart.
		t.parts = append(t.parts, templatePart{})
	}
	return t
}

// storedTemplate builds the template of a route read back from the store.
//
// The save rules are not applied again: a link that was saved keeps working
// when a rule is added later (the credentials rule arrived after the first
// release). What must hold is only what expansion itself depends on: the
// braces are well-formed, every placeholder names a variable of the pattern
// and stands after the host, and the URL parses. A destination without
// placeholders is taken as it is, whatever it contains.
func storedTemplate(dest string, p Pattern) (Template, bool) {
	if !strings.ContainsAny(dest, "{}") {
		if dest == "" {
			return Template{}, false
		}
		return Template{text: dest, parts: []templatePart{{literal: dest}}}, true
	}
	t, problems := parseDestination(dest, p, ValidateOptions{})
	for _, problem := range problems {
		switch problem.Code {
		case CodeDestinationUnusedVariable, CodeDestinationCredentials,
			CodeDestinationGoLink, CodeDestinationDirectory, CodeDestinationTooLong:
			// Rules about what may be saved, not about what can be expanded.
		default:
			return Template{}, false
		}
	}
	if len(t.parts) == 0 {
		// Only save rules stood in the way; parse again without them.
		return lenientTemplate(dest)
	}
	return t, true
}

// lenientTemplate is the part of parseDestination that expansion needs, for
// a stored destination that breaks a save rule.
func lenientTemplate(dest string) (Template, bool) {
	placeholders, ok := scanPlaceholders(dest, func(string, string, int, int) {})
	if !ok {
		return Template{}, false
	}
	u, err := url.Parse(probe(dest, placeholders))
	if err != nil || u.Host == "" {
		return Template{}, false
	}
	return newTemplate(dest, placeholders, u), true
}

// scanPlaceholders finds the placeholders of dest and reports every brace
// that is not part of one. ok is false when it reported anything.
//
// Every "{" starts a placeholder and every "}" ends one: a destination has
// no other use for a brace, and a literal one is written %7B or %7D. The
// section a placeholder is in follows from the text before it: after the
// first "#" it is the fragment, after the first "?" the query, else the
// path. (Whether it is before the path altogether is the caller's check.)
func scanPlaceholders(dest string, add func(code, message string, start, end int)) (found []placeholder, ok bool) {
	ok = true
	where := inPath
	closingLeft := true // false once a search for "}" has found none
	for i := 0; i < len(dest); i++ {
		switch dest[i] {
		case '?':
			if where == inPath {
				where = inQuery
			}
		case '#':
			where = inFragment
		case '}':
			ok = false
			add(CodeDestinationUnmatchedBrace,
				"This } has no { before it. Write a literal brace as %7D.", i, i+1)
		case '{':
			closing := -1
			if closingLeft {
				// Searched once: after a failure no later "{" can have a
				// closing brace either, and searching again for each would
				// cost time quadratic in the length.
				closing = strings.IndexByte(dest[i:], '}')
				closingLeft = closing >= 0
			}
			if closing < 0 {
				ok = false
				end := i + 1
				for end < len(dest) && isVariableNameByte(dest[end]) {
					end++
				}
				add(CodeDestinationUnclosedPlaceholder,
					"This placeholder is missing its closing }. Write a literal brace as %7B.", i, end)
				continue
			}
			end := i + closing + 1
			name := dest[i+1 : end-1]
			if !validVariableName(name) {
				ok = false
				add(CodeDestinationBadPlaceholder, badPlaceholderMessage(name), i, end)
			} else {
				found = append(found, placeholder{name: name, start: i, end: end, where: where})
			}
			i = end - 1
		}
	}
	return found, ok
}

// badPlaceholderMessage explains braces that do not hold a variable name.
func badPlaceholderMessage(inside string) string {
	const literal = " Write a literal brace as %7B or %7D."
	switch {
	case inside == "":
		return "Write a variable's name between the braces, such as {name}." + literal
	case strings.ContainsRune(inside, ':'):
		return "A placeholder is only the variable's name, such as {name}. Its type belongs in the pattern."
	case strings.ContainsAny(inside, "{"):
		return "A placeholder is written with one pair of braces, such as {name}." + literal
	}
	return quoteForMessage(inside) + " is not a variable name, so it can't be a placeholder." + literal
}

// undeclaredMessage explains a placeholder that the pattern has no variable
// for, and says which variables there are.
func undeclaredMessage(name string, p Pattern) string {
	for _, v := range p.variables {
		if strings.EqualFold(v, name) {
			return fmt.Sprintf("The pattern has no variable {%s}. Its variable is spelled {%s}; upper and lower case matter.", name, v)
		}
	}
	switch len(p.variables) {
	case 0:
		return fmt.Sprintf("The pattern has no variable {%s}; it has no variables at all. "+
			"Add {%s} to the pattern, or write a literal brace as %%7B or %%7D.", name, name)
	case 1:
		return fmt.Sprintf("The pattern has no variable {%s}. Its only variable is {%s}.", name, p.variables[0])
	}
	return fmt.Sprintf("The pattern has no variable {%s}. Its variables are {%s}.",
		name, strings.Join(p.variables, "}, {"))
}

// probe returns dest with every placeholder replaced by a letter, so that a
// URL parser can check the rest. The letter stands for any value: a value is
// inserted as unreserved characters and percent escapes, which are valid
// wherever a placeholder may stand.
func probe(dest string, placeholders []placeholder) string {
	if len(placeholders) == 0 {
		return dest
	}
	var b strings.Builder
	at := 0
	for _, ph := range placeholders {
		b.WriteString(dest[at:ph.start])
		b.WriteByte('x')
		at = ph.end
	}
	b.WriteString(dest[at:])
	return b.String()
}

// bareHost returns the URL's host name in lowercase, without port and
// without the trailing dot of a fully qualified name.
func bareHost(u *url.URL) string {
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// canonicalHost returns the one spelling of a host that every other spelling
// of the same address is compared by. A browser does not ask for the host as
// it is written: it rewrites "127.1", "2130706433", "0x7f.0.0.1" and
// "017700000001" to "127.0.0.1" and "[0:0:0:0:0:0:0:1]" to "[::1]" before it
// connects and before it fills in the Host header. A rule about "the
// Directory itself" that compared the written text would therefore be passed
// by writing the address another way (PRD 5.4).
//
// An IPv6 address is brought to its shortest form, and an IPv4 address
// written inside one ("::ffff:127.0.0.1") to the IPv4 address. A host that a
// browser would read as an IPv4 address is brought to four decimal numbers.
// Any other host is a name, returned in lowercase without a trailing dot.
//
// Not covered: a name written with Unicode look-alikes of ASCII letters
// (fullwidth "ｌｏｃａｌｈｏｓｔ"), which a browser also maps to the plain name.
// Recognising those needs the IDNA mapping tables, which the standard library
// does not have.
func canonicalHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if strings.Contains(host, ":") {
		// IPv6, as url.URL.Hostname returns it: without the brackets.
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap().WithZone("").String()
		}
		return host
	}
	if v4, ok := browserIPv4(host); ok {
		return netip.AddrFrom4(v4).String()
	}
	return host
}

// browserIPv4 reads host the way a browser's URL parser reads an IPv4
// address (WHATWG URL, "IPv4 parser"): one to four numbers separated by dots,
// each decimal, hexadecimal ("0x7f") or octal (a leading "0"); every number
// but the last is one byte, and the last fills all the bytes that remain, so
// "127.1" is 127.0.0.1 and "2130706433" is the same address.
//
// ok is false for anything else, which is then a host name. That includes
// text a browser refuses outright, such as "256.1.1.1": no browser can be
// sent there, so it cannot be the Directory either.
func browserIPv4(host string) (addr [4]byte, ok bool) {
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return addr, false
	}
	var numbers [4]uint64
	for i, part := range parts {
		base := 10
		switch {
		case strings.HasPrefix(part, "0x"): // host is already lowercase
			part, base = part[2:], 16
			if part == "" {
				part = "0" // a browser reads a bare "0x" as zero
			}
		case len(part) > 1 && part[0] == '0':
			part, base = part[1:], 8
		}
		// ParseUint alone would also take "+1" and "1_0".
		if part == "" || strings.ContainsAny(part, "+-_") {
			return addr, false
		}
		n, err := strconv.ParseUint(part, base, 32)
		if err != nil {
			return addr, false
		}
		numbers[i] = n
	}
	last := len(parts) - 1
	for i := 0; i < last; i++ {
		if numbers[i] > 255 {
			return addr, false
		}
		addr[i] = byte(numbers[i])
	}
	// The last number fills the remaining 4-last bytes.
	rest := numbers[last]
	if room := uint(8 * (4 - last)); room < 32 && rest >= 1<<room {
		return addr, false
	}
	for i := 3; i >= last; i-- {
		addr[i] = byte(rest)
		rest >>= 8
	}
	return addr, true
}

// ownHost reports whether u is on one of the Directory's own hosts (see
// ValidateOptions.OwnHosts). Both sides are compared in canonical form
// (canonicalHost, canonicalPort), so no other spelling of the Directory's
// address gets past.
func ownHost(u *url.URL, own []string) bool {
	if len(own) == 0 {
		return false
	}
	host, port := canonicalHost(u.Hostname()), effectivePort(u.Port(), u.Scheme)
	for _, entry := range own {
		if !strings.Contains(entry, "://") {
			entry = "//" + entry
		}
		o, err := url.Parse(entry)
		if err != nil || o.Hostname() == "" || canonicalHost(o.Hostname()) != host {
			continue
		}
		scheme := o.Scheme
		if scheme == "" {
			scheme = u.Scheme // a bare host: the default port of whatever is asked for
		}
		if effectivePort(o.Port(), scheme) == port {
			return true
		}
	}
	return false
}

// effectivePort returns the port a URL connects to, as a number in its one
// decimal spelling: the one written ("04711" is 4711), or the scheme's
// default. It is "" when there is neither.
func effectivePort(port, scheme string) string {
	switch {
	case port != "":
		if n, err := strconv.Atoi(port); err == nil {
			return strconv.Itoa(n)
		}
		return port
	case strings.EqualFold(scheme, "http"):
		return "80"
	case strings.EqualFold(scheme, "https"):
		return "443"
	}
	return ""
}

// isHexDigit reports whether c is a hexadecimal digit.
func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// isVariableNameByte reports whether c may appear in a variable name.
func isVariableNameByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_'
}

// firstBadCharacter finds the first byte range of s that is whitespace, a
// control character, an invisible formatting character or not valid UTF-8.
//
// The check covers Unicode, not only ASCII: U+2028 LINE SEPARATOR and U+0085
// NEXT LINE are line breaks to some software, a no-break space looks like the
// space it is not, and the formatting characters (category Cf, which holds
// the bidirectional overrides such as U+202E and the zero-width characters)
// let a destination display as something other than where it leads.
func firstBadCharacter(s string) (start, end int, found bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		invalid := r == utf8.RuneError && size == 1
		if invalid || unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return i, i + size, true
		}
		i += size
	}
	return 0, 0, false
}

// validPort reports whether a URL's port, as url.URL.Port returns it, is
// absent or a number a connection can actually be made to. url.Parse accepts
// any run of digits.
func validPort(port string) bool {
	if port == "" {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// leadingScheme returns the URL scheme s begins with (RFC 3986: a letter
// followed by letters, digits, "+", "-" or ".", then ":"). It is read from
// the text rather than taken from url.Parse so that "example.com/x" and
// "ftp://example.com" get different, specific messages even when the rest of
// the URL would not parse.
func leadingScheme(s string) (scheme string, ok bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		case i > 0 && c == ':':
			return s[:i], true
		default:
			return "", false
		}
	}
	return "", false
}
