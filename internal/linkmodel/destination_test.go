package linkmodel

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestValidateDestinationValid(t *testing.T) {
	longest := "https://example.com/" + strings.Repeat("a", MaxDestinationLength-len("https://example.com/"))
	tests := []struct {
		name string
		dest string
	}{
		{"https", "https://example.com"},
		{"http", "http://example.com"},
		{"path", "https://example.com/roadmap/2026"},
		{"trailing slash", "https://example.com/"},
		{"query and fragment", "https://example.com/search?q=go+links&page=2#results"},
		{"port", "http://localhost:8080/x"},
		{"IPv4 host", "http://127.0.0.1:9000/"},
		{"IPv6 host", "http://[::1]:9000/x"},
		{"single-label host", "http://wiki/Home"},
		{"upper-case scheme", "HTTPS://EXAMPLE.COM/Path"},
		{"credentials", "https://user:secret@example.com/"},
		{"percent escapes", "https://example.com/a%20b%2Fc"},
		{"encoded braces", "https://example.com/%7Bname%7D"},
		{"non-ASCII path", "https://example.com/café"},
		{"non-ASCII query and fragment", "https://example.com/żółć?q=ü#日本語"},
		{"highest port", "https://example.com:65535/"},
		{"exactly 2048 characters", longest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if problems := ValidateDestination(tt.dest); problems != nil {
				t.Errorf("ValidateDestination(%q) = %+v, want no problems", tt.dest, problems)
			}
		})
	}
}

func TestValidateDestinationProblems(t *testing.T) {
	type want struct {
		code       string
		start, end int
	}
	tooLong := "https://example.com/" + strings.Repeat("a", MaxDestinationLength-len("https://example.com/")+1)
	tests := []struct {
		name string
		dest string
		want []want
	}{
		{"empty", "", []want{{CodeDestinationEmpty, 0, 0}}},

		// Not absolute: no scheme at all.
		{"host and path only", "example.com/x", []want{{CodeDestinationNotAbsolute, 0, 13}}},
		{"absolute path", "/roadmap", []want{{CodeDestinationNotAbsolute, 0, 8}}},
		{"scheme-relative", "//example.com/x", []want{{CodeDestinationNotAbsolute, 0, 15}}},
		{"a word", "roadmap", []want{{CodeDestinationNotAbsolute, 0, 7}}},
		{"go link", "go/roadmap", []want{{CodeDestinationNotAbsolute, 0, 10}}},
		{"colon first", "://example.com", []want{{CodeDestinationNotAbsolute, 0, 14}}},

		// A scheme other than http or https.
		{"ftp", "ftp://example.com/x", []want{{CodeDestinationScheme, 0, 3}}},
		{"javascript", "javascript:alert(1)", []want{{CodeDestinationScheme, 0, 10}}},
		{"data", "data:text/html,hi", []want{{CodeDestinationScheme, 0, 4}}},
		{"file", "file:///etc/passwd", []want{{CodeDestinationScheme, 0, 4}}},
		{"mailto", "mailto:a@example.com", []want{{CodeDestinationScheme, 0, 6}}},
		{"chrome", "chrome://settings", []want{{CodeDestinationScheme, 0, 6}}},
		{"host and port without a scheme", "localhost:4655/x", []want{{CodeDestinationScheme, 0, 9}}},
		{"httpx", "httpx://example.com", []want{{CodeDestinationScheme, 0, 5}}},
		// The scheme is compared ignoring case, so an upper-case one is no
		// way around the rule.
		{"upper-case ftp", "FTP://example.com/x", []want{{CodeDestinationScheme, 0, 3}}},
		{"mixed-case javascript", "JavaScript:alert(1)", []want{{CodeDestinationScheme, 0, 10}}},

		// http or https, but no host.
		{"scheme only", "https://", []want{{CodeDestinationNoHost, 0, 8}}},
		{"no authority", "http:/x", []want{{CodeDestinationNoHost, 0, 7}}},
		{"opaque", "http:example.com", []want{{CodeDestinationNoHost, 0, 16}}},
		{"empty host with a path", "https:///x", []want{{CodeDestinationNoHost, 0, 10}}},
		{"port without a host", "http://:8080/", []want{{CodeDestinationNoHost, 0, 13}}},
		{"query without a host", "https://?q=1", []want{{CodeDestinationNoHost, 0, 12}}},

		// Unparseable.
		{"bad port", "http://example.com:port/", []want{{CodeDestinationInvalid, 0, 24}}},
		{"unclosed IPv6 bracket", "http://[::1/x", []want{{CodeDestinationInvalid, 0, 13}}},
		{"bad escape", "http://example.com/%zz", []want{{CodeDestinationInvalid, 0, 22}}},
		{"port above 65535", "https://example.com:99999/", []want{{CodeDestinationInvalid, 0, 26}}},
		{"port 65536", "https://example.com:65536/", []want{{CodeDestinationInvalid, 0, 26}}},
		{"port 0", "https://example.com:0/", []want{{CodeDestinationInvalid, 0, 22}}},
		{"port too long for an int", "https://example.com:99999999999999999999/", []want{{CodeDestinationInvalid, 0, 41}}},

		// Whitespace and control characters.
		{"space in the path", "https://example.com/a b", []want{{CodeDestinationBadCharacter, 21, 22}}},
		{"leading space", " https://example.com", []want{{CodeDestinationBadCharacter, 0, 1}}},
		{"trailing newline", "https://example.com\n", []want{{CodeDestinationBadCharacter, 19, 20}}},
		{"header injection", "https://example.com/\r\nSet-Cookie: a=b", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"tab", "https://example.com/\tx", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"NUL", "https://example.com/\x00", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"DEL", "https://example.com/\x7f", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"invalid UTF-8", "https://example.com/\xff", []want{{CodeDestinationBadCharacter, 20, 21}}},

		// The same, beyond ASCII: line breaks, spaces and invisible
		// formatting characters that Unicode adds.
		{"line separator U+2028", "https://example.com/\u2028x", []want{{CodeDestinationBadCharacter, 20, 23}}},
		{"paragraph separator U+2029", "https://example.com/\u2029x", []want{{CodeDestinationBadCharacter, 20, 23}}},
		{"next line U+0085", "https://example.com/\u0085x", []want{{CodeDestinationBadCharacter, 20, 22}}},
		{"no-break space U+00A0", "https://example.com/\u00a0x", []want{{CodeDestinationBadCharacter, 20, 22}}},
		{"ideographic space U+3000", "https://example.com/\u3000x", []want{{CodeDestinationBadCharacter, 20, 23}}},
		{"right-to-left override U+202E", "https://example.com/\u202egpj.exe", []want{{CodeDestinationBadCharacter, 20, 23}}},
		{"isolate U+2066", "https://example.com/\u2066x", []want{{CodeDestinationBadCharacter, 20, 23}}},
		{"zero-width space U+200B", "https://exam\u200bple.com/", []want{{CodeDestinationBadCharacter, 12, 15}}},
		{"byte order mark U+FEFF", "\ufeffhttps://example.com/", []want{{CodeDestinationBadCharacter, 0, 3}}},
		{"C1 control U+009F", "https://example.com/\u009f", []want{{CodeDestinationBadCharacter, 20, 22}}},

		// Placeholders arrive in L2.
		{"placeholder", "https://example.com/{repo}", []want{{CodeDestinationPlaceholderUnsupported, 20, 26}}},
		{"placeholder in the query", "https://example.com/?q={q}&x=1", []want{{CodeDestinationPlaceholderUnsupported, 23, 26}}},
		{"placeholder in the host", "https://{team}.example.com/", []want{{CodeDestinationPlaceholderUnsupported, 8, 14}}},
		{"unclosed brace", "https://example.com/{repo", []want{{CodeDestinationPlaceholderUnsupported, 20, 21}}},
		{"closing brace alone", "https://example.com/repo}", []want{{CodeDestinationPlaceholderUnsupported, 24, 25}}},
		{"placeholder and a wrong scheme", "ftp://example.com/{x}", []want{
			{CodeDestinationScheme, 0, 3}, {CodeDestinationPlaceholderUnsupported, 18, 21}}},
		{"placeholder only", "{url}", []want{
			{CodeDestinationPlaceholderUnsupported, 0, 5}, {CodeDestinationNotAbsolute, 0, 5}}},

		// Length.
		{"2049 characters", tooLong, []want{{CodeDestinationTooLong, 2048, 2049}}},
		{"too long and not a URL", strings.Repeat("a", 2050), []want{
			{CodeDestinationNotAbsolute, 0, 2050}, {CodeDestinationTooLong, 2048, 2050}}},
		{"too long with a space before the limit", "https://example.com/a b" + strings.Repeat("a", 2048), []want{
			{CodeDestinationBadCharacter, 21, 22}, {CodeDestinationTooLong, 2048, 2071}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidateDestination(tt.dest)
			var got []want
			for _, p := range problems {
				got = append(got, want{p.Code, p.Start, p.End})
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ValidateDestination(%q) problems =\n  %+v\nwant\n  %+v", tt.dest, got, tt.want)
			}
			checkProblems(t, tt.dest, FieldDestination, problems)
		})
	}
}

func FuzzValidateDestination(f *testing.F) {
	for _, seed := range []string{
		"", "https://example.com", "http://[::1]:9000/x", "example.com", "ftp://x",
		"javascript:alert(1)", "https://", "https://example.com/{x}", "http://a b/",
		"https://example.com/\r\n", "http://example.com:port/", "\xff", ":", "http:",
		"FTP://example.com/x", "JavaScript:alert(1)", "https://example.com:99999/",
		"https://example.com/\u2028x", "https://example.com/\u202ex", "https://example.com/\u00a0x",
		"https://example.com/żółć?q=ü#frag", "https://user:secret@example.com/",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, dest string) {
		problems := ValidateDestination(dest)
		checkProblems(t, dest, FieldDestination, problems)
		if len(problems) > 0 {
			return
		}
		// Whatever is accepted is safe to send in a Location header.
		lower := strings.ToLower(dest)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			t.Fatalf("accepted %q, which is not an http or https URL", dest)
		}
		if strings.ContainsAny(dest, " \t\r\n{}\x00\x7f") {
			t.Fatalf("accepted %q, which contains a forbidden character", dest)
		}
		for _, r := range dest {
			if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("accepted %q, which contains the invisible or spacing character %U", dest, r)
			}
		}
		if !utf8.ValidString(dest) {
			t.Fatalf("accepted %q, which is not valid UTF-8", dest)
		}
		u, err := url.Parse(dest)
		if err != nil || u.Hostname() == "" {
			t.Fatalf("accepted %q, which has no host (%v)", dest, err)
		}
		if port := u.Port(); port != "" {
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				t.Fatalf("accepted %q, whose port %q is not a real port", dest, port)
			}
		}
	})
}
