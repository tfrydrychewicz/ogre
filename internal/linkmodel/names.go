package linkmodel

import "strings"

// NormalizeName returns the form of a link name used for storage and lookup.
//
// Lowercasing is the only normalization (PRD question Q5): "On-Call" and
// "on-call" are the same link, while "on-call", "on_call" and "oncall" are
// three different names.
//
// Only the ASCII letters A-Z are folded. Names are ASCII by rule (PRD 5.2), so
// full Unicode case mapping could only make a request that is not a valid name
// collide with one that is: U+212A KELVIN SIGN lowercases to "k".
func NormalizeName(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			return lowerASCIIFrom(s, i)
		}
	}
	return s // the common case: nothing to fold, nothing allocated
}

// lowerASCIIFrom lowercases s, whose first upper-case letter is at index i.
func lowerASCIIFrom(s string, i int) string {
	b := []byte(s)
	for ; i < len(b); i++ {
		if c := b[i]; 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// equalFoldASCII reports whether a and b are equal ignoring the case of ASCII
// letters. It is NormalizeName(a) == NormalizeName(b) without allocating,
// because it runs on every resolved request.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerByte(a[i]) != lowerByte(b[i]) {
			return false
		}
	}
	return true
}

func lowerByte(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// PossibleName reports whether s could be the name of a link: it is a
// literal as PRD 5.2 defines one (ASCII letters, digits, "-", "_" and ".",
// beginning with a letter or digit) of at most MaxPatternLength characters.
// Case is not checked, because lookup ignores it.
//
// The resolver asks this before it repeats a requested name anywhere. The
// first segment of a request is arbitrary text from a URL: after decoding it
// may hold slashes, line breaks or markup, or be as long as the request line
// allows. Only text that could be a name is echoed in a response or recorded
// in a log (NFR-17: a miss records the requested name, never more of the
// path).
func PossibleName(s string) bool {
	if s == "" || len(s) > MaxPatternLength || !isLiteralStart(rune(s[0])) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isLiteralRune(rune(s[i])) {
			return false
		}
	}
	return true
}

// reservedExact lists first segments that browsers and crawlers request on
// their own, without the user typing a go link.
var reservedExact = map[string]bool{
	"favicon.ico": true,
	"robots.txt":  true,
	"sitemap.xml": true,
	".well-known": true,
}

// ReservedName reports whether name, as the first segment of a path, is one
// that browsers request unprompted: favicon.ico, robots.txt, sitemap.xml,
// apple-touch-icon*.png and .well-known (plan section 3.3).
//
// Such a path must never be treated as a go link: a link named "favicon.ico"
// would be followed by every tab that asks for the site icon, and its misses
// would drown the real ones. The server answers these paths itself and
// ValidatePattern refuses them as link names. The comparison ignores case, as
// name lookup does.
func ReservedName(name string) bool {
	n := NormalizeName(name)
	if reservedExact[n] {
		return true
	}
	return strings.HasPrefix(n, "apple-touch-icon") && strings.HasSuffix(n, ".png")
}
