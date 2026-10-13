package linkmodel

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDestinationLength is the longest destination accepted, in characters
// (PRD 5.4). Browsers and proxies handle URLs of this size reliably.
const MaxDestinationLength = 2048

// Problem codes reported by ValidateDestination. Like the pattern codes they
// are part of the API.
const (
	CodeDestinationEmpty                  = "destination.empty"
	CodeDestinationTooLong                = "destination.too_long"
	CodeDestinationBadCharacter           = "destination.bad_character"
	CodeDestinationPlaceholderUnsupported = "destination.placeholder_unsupported"
	CodeDestinationNotAbsolute            = "destination.not_absolute"
	CodeDestinationScheme                 = "destination.scheme"
	CodeDestinationNoHost                 = "destination.no_host"
	CodeDestinationInvalid                = "destination.invalid"
)

// ValidateDestination checks where a route redirects to.
//
// A destination is an absolute http or https URL with a host, of at most
// MaxDestinationLength characters (PRD 5.4). Restricting the scheme is a
// safety rule, not a convenience: the resolver sends the destination in a
// Location header, and a "javascript:" or "file:" destination would turn a
// go link into a way to run script or open local files.
//
// Whitespace, control characters and invisible formatting characters are
// refused outright, in all of Unicode (see firstBadCharacter). A URL never
// needs them unencoded, and a line break in a header value is how response
// splitting starts. Other non-ASCII text is allowed and kept exactly as
// written; the HTTP layer percent-encodes it when it sends the destination.
//
// Credentials ("https://user:secret@host/") are still accepted in this first
// cut. PRD 5.4 forbids them; the check arrives with L2.4.
//
// L1 first cut: placeholders arrive in L2, so "{" and "}" are reported as
// CodeDestinationPlaceholderUnsupported. The rule that a destination may not
// be a go link or the Directory itself also belongs to L2.
//
// It returns nil when the destination is valid; otherwise the problems
// ordered by position, each with the byte range it concerns.
func ValidateDestination(dest string) []Problem {
	var problems []Problem
	add := func(code, message string, start, end int) {
		problems = append(problems, Problem{
			Severity: SeverityError, Field: FieldDestination,
			Code: code, Message: message, Start: start, End: end,
		})
	}

	if dest == "" {
		add(CodeDestinationEmpty, "Enter the URL this link should open.", 0, 0)
		return problems
	}
	if over := offsetOfRune(dest, MaxDestinationLength); over >= 0 {
		add(CodeDestinationTooLong, fmt.Sprintf(
			"A destination can have at most %d characters; this one has %d.",
			MaxDestinationLength, utf8.RuneCountInString(dest)), over, len(dest))
	}
	if start, end, found := firstBadCharacter(dest); found {
		add(CodeDestinationBadCharacter,
			"A destination can't contain spaces, line breaks, control characters or invisible characters. "+
				"Write a space as %20.", start, end)
		return byPosition(problems) // nothing after this can be parsed reliably
	}

	placeholder := false
	if start, end, found := firstBrace(dest); found {
		placeholder = true
		add(CodeDestinationPlaceholderUnsupported,
			"Placeholders such as {name} are not supported yet. "+
				"Write a literal brace as %7B or %7D.", start, end)
	}

	scheme, ok := leadingScheme(dest)
	switch {
	case !ok:
		add(CodeDestinationNotAbsolute,
			"A destination must be a full URL that starts with http:// or https://.", 0, len(dest))
	case !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https"):
		add(CodeDestinationScheme, fmt.Sprintf(
			"A destination must start with http:// or https://, not %q.", scheme+":"), 0, len(scheme))
	case placeholder:
		// A brace can make the URL unparseable; the placeholder problem
		// already says what to fix.
	default:
		u, err := url.Parse(dest)
		switch {
		case err != nil:
			add(CodeDestinationInvalid,
				"This is not a valid URL. Check the host, the port and any % escapes.", 0, len(dest))
		case u.Hostname() == "":
			add(CodeDestinationNoHost,
				"The destination has no host. Write it like https://example.com/page.", 0, len(dest))
		case !validPort(u.Port()):
			add(CodeDestinationInvalid,
				"This is not a valid URL. Check the host, the port and any % escapes.", 0, len(dest))
		}
	}

	return byPosition(problems)
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

// firstBrace finds the first "{" or "}" of s. For an opening brace the range
// extends to its closing brace, so the whole placeholder is marked.
func firstBrace(s string) (start, end int, found bool) {
	start = strings.IndexAny(s, "{}")
	if start < 0 {
		return 0, 0, false
	}
	end = start + 1
	if s[start] == '{' {
		if closing := strings.IndexByte(s[start:], '}'); closing >= 0 {
			end = start + closing + 1
		}
	}
	return start, end, true
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
