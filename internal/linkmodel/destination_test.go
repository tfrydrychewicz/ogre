package linkmodel

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// noPattern is what ParseDestination is given when the route's pattern did
// not parse: placeholders are then checked for their form only.
var noPattern Pattern

func TestParseDestinationValid(t *testing.T) {
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
		{"percent escapes", "https://example.com/a%20b%2Fc"},
		{"encoded braces", "https://example.com/%7Bname%7D"},
		{"non-ASCII path", "https://example.com/café"},
		{"non-ASCII query and fragment", "https://example.com/żółć?q=ü#日本語"},
		{"highest port", "https://example.com:65535/"},
		{"exactly 2048 characters", longest},

		// An "@" after the host is not a credential.
		{"at sign in the path", "https://example.com/@user"},
		{"at sign in the query", "https://example.com/?to=a@example.com"},
		{"at sign in the fragment", "https://example.com/#@x"},
		{"at sign in a query without a path", "https://example.com?to=a@b"},

		// Hosts that merely resemble a go link.
		{"host begins with go", "https://go.example.com/x"},
		{"host golang", "https://golang.org/"},
		{"go as a path", "https://example.com/go/x"},
		{"host ends with go", "https://example.go/"},

		// A placeholder next to an escape that is complete without it.
		{"placeholder after a whole escape", "https://example.com/?q=%41{a}"},
		{"placeholder after an encoded percent sign", "https://example.com/?q=%25{a}"},
		{"placeholder before an escape", "https://example.com/{a}%41"},
		{"percent sign in a static query", "https://example.com/?q=100%"},

		// Without a pattern, a placeholder only has to be well-formed.
		{"placeholder in the path", "https://example.com/{repo}"},
		{"placeholder in the query", "https://example.com/?q={q}&x=1"},
		{"placeholder in the fragment", "https://example.com/#{section}"},
		{"placeholder right after the host's slash", "https://example.com/{a}/{b}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, problems := ParseDestination(tt.dest, noPattern)
			if problems != nil {
				t.Fatalf("ParseDestination(%q) = %+v, want no problems", tt.dest, problems)
			}
			if tmpl.String() != tt.dest {
				t.Errorf("Template.String() = %q, want the destination as written", tmpl.String())
			}
		})
	}
}

func TestParseDestinationProblems(t *testing.T) {
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
		{"go link written short", "go/roadmap", []want{{CodeDestinationNotAbsolute, 0, 10}}},
		{"colon first", "://example.com", []want{{CodeDestinationNotAbsolute, 0, 14}}},

		// A scheme other than http or https (NFR-9).
		{"ftp", "ftp://example.com/x", []want{{CodeDestinationScheme, 0, 3}}},
		{"javascript", "javascript:alert(1)", []want{{CodeDestinationScheme, 0, 10}}},
		{"data", "data:text/html,hi", []want{{CodeDestinationScheme, 0, 4}}},
		{"file", "file:///etc/passwd", []want{{CodeDestinationScheme, 0, 4}}},
		{"mailto", "mailto:a@example.com", []want{{CodeDestinationScheme, 0, 6}}},
		{"chrome", "chrome://settings", []want{{CodeDestinationScheme, 0, 6}}},
		{"vbscript", "vbscript:msgbox(1)", []want{{CodeDestinationScheme, 0, 8}}},
		{"blob", "blob:https://example.com/uuid", []want{{CodeDestinationScheme, 0, 4}}},
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
		{"backslashes for slashes", `https:\\example.com\x`, []want{{CodeDestinationNoHost, 0, 21}}},
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
		// A browser reads "\" as "/" and would stop the host there; Go does
		// not. Refusing it keeps the two from disagreeing about the host.
		{"backslash in the host", `https://example.com\x.evil.example/`, []want{{CodeDestinationInvalid, 0, 35}}},
		{"backslash before the host ends", `https://go\.example.com/`, []want{{CodeDestinationInvalid, 0, 24}}},
		{"escape in the host", "https://ex%41mple.com/", []want{{CodeDestinationInvalid, 0, 22}}},

		// Whitespace and control characters.
		{"space in the path", "https://example.com/a b", []want{{CodeDestinationBadCharacter, 21, 22}}},
		{"leading space", " https://example.com", []want{{CodeDestinationBadCharacter, 0, 1}}},
		{"trailing newline", "https://example.com\n", []want{{CodeDestinationBadCharacter, 19, 20}}},
		{"header injection", "https://example.com/\r\nSet-Cookie: a=b", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"tab", "https://example.com/\tx", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"NUL", "https://example.com/\x00", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"DEL", "https://example.com/\x7f", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"invalid UTF-8", "https://example.com/\xff", []want{{CodeDestinationBadCharacter, 20, 21}}},
		{"space inside a placeholder", "https://example.com/{a b}", []want{{CodeDestinationBadCharacter, 22, 23}}},

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

		// Credentials (PRD 5.4).
		{"user and password", "https://user:secret@example.com/", []want{{CodeDestinationCredentials, 8, 20}}},
		{"user only", "https://user@example.com/", []want{{CodeDestinationCredentials, 8, 13}}},
		{"looks like example.com, opens evil.example", "https://example.com@evil.example/", []want{{CodeDestinationCredentials, 8, 20}}},
		{"empty user", "https://@example.com/", []want{{CodeDestinationCredentials, 8, 9}}},
		{"password only", "https://:secret@example.com/", []want{{CodeDestinationCredentials, 8, 16}}},
		{"credentials and a port", "http://a:b@example.com:8080/x", []want{{CodeDestinationCredentials, 7, 11}}},
		{"two at signs", "https://a@b@example.com/", []want{{CodeDestinationCredentials, 8, 12}}},
		{"encoded at sign in the user", "https://a%40b@example.com/", []want{{CodeDestinationCredentials, 8, 14}}},
		{"credentials without a path", "https://user@example.com", []want{{CodeDestinationCredentials, 8, 13}}},
		{"credentials before a query", "https://user@example.com?x=1", []want{{CodeDestinationCredentials, 8, 13}}},
		{"credentials with a backslash", `https://example.com\@evil.example/`, []want{{CodeDestinationCredentials, 8, 21}}},
		{"credentials hiding a go link", "https://user@go/x", []want{{CodeDestinationCredentials, 8, 13}}},

		// A go link as a destination (PRD 5.4).
		{"go link", "http://go/roadmap", []want{{CodeDestinationGoLink, 7, 9}}},
		{"go link over https", "https://go/roadmap", []want{{CodeDestinationGoLink, 8, 10}}},
		{"go link in upper case", "http://GO/roadmap", []want{{CodeDestinationGoLink, 7, 9}}},
		{"go link, mixed case", "HTTP://Go/roadmap", []want{{CodeDestinationGoLink, 7, 9}}},
		{"go link without a path", "http://go", []want{{CodeDestinationGoLink, 7, 9}}},
		{"go link with a port", "http://go:8080/x", []want{{CodeDestinationGoLink, 7, 14}}},
		{"go link, fully qualified", "http://go./x", []want{{CodeDestinationGoLink, 7, 10}}},
		{"go link with a placeholder", "http://go/{name}", []want{{CodeDestinationGoLink, 7, 9}}},
		{"go link with a query", "http://go?x=1", []want{{CodeDestinationGoLink, 7, 9}}},

		// Braces that do not make a placeholder.
		{"unclosed placeholder", "https://example.com/{repo", []want{{CodeDestinationUnclosedPlaceholder, 20, 25}}},
		{"unclosed, text follows", "https://example.com/{repo/x", []want{{CodeDestinationUnclosedPlaceholder, 20, 25}}},
		{"only an opening brace", "https://example.com/{", []want{{CodeDestinationUnclosedPlaceholder, 20, 21}}},
		{"closing brace alone", "https://example.com/repo}", []want{{CodeDestinationUnmatchedBrace, 24, 25}}},
		{"doubled braces", "https://example.com/{{repo}}", []want{
			{CodeDestinationBadPlaceholder, 20, 27}, {CodeDestinationUnmatchedBrace, 27, 28}}},
		{"two opening braces", "https://example.com/{{", []want{
			{CodeDestinationUnclosedPlaceholder, 20, 21}, {CodeDestinationUnclosedPlaceholder, 21, 22}}},
		{"empty placeholder", "https://example.com/{}", []want{{CodeDestinationBadPlaceholder, 20, 22}}},
		{"name begins with a digit", "https://example.com/{1}", []want{{CodeDestinationBadPlaceholder, 20, 23}}},
		{"type in a placeholder", "https://example.com/{id:int}", []want{{CodeDestinationBadPlaceholder, 20, 28}}},
		{"plus prefix of a later milestone", "https://example.com/{+rest}", []want{{CodeDestinationBadPlaceholder, 20, 27}}},
		{"slash in a placeholder", "https://example.com/{a/b}", []want{{CodeDestinationBadPlaceholder, 20, 25}}},
		{"JSON in a query", `https://example.com/?filter={"a":1}`, []want{{CodeDestinationBadPlaceholder, 28, 35}}},

		// A placeholder before the path (PRD 5.4, NFR-10).
		{"placeholder is the subdomain", "https://{team}.example.com/", []want{{CodeDestinationPlaceholderInHost, 8, 14}}},
		{"placeholder is the host", "https://{host}/x", []want{{CodeDestinationPlaceholderInHost, 8, 14}}},
		{"placeholder ends the host", "https://example.{tld}/x", []want{{CodeDestinationPlaceholderInHost, 16, 21}}},
		{"placeholder glued to the host", "https://example.com{path}", []want{{CodeDestinationPlaceholderInHost, 19, 25}}},
		{"placeholder is the port", "https://example.com:{port}/x", []want{{CodeDestinationPlaceholderInHost, 20, 26}}},
		{"placeholder is the user", "https://{user}@example.com/", []want{
			{CodeDestinationPlaceholderInHost, 8, 14}, {CodeDestinationCredentials, 8, 15}}},
		{"placeholder is the password", "https://u:{pw}@example.com/", []want{
			{CodeDestinationCredentials, 8, 15}, {CodeDestinationPlaceholderInHost, 10, 14}}},
		{"placeholder is the scheme", "{scheme}://example.com/x", []want{
			{CodeDestinationNotAbsolute, 0, 24}, {CodeDestinationPlaceholderInHost, 0, 8}}},
		{"placeholder inside the scheme", "http{s}://example.com/x", []want{
			{CodeDestinationNotAbsolute, 0, 23}, {CodeDestinationPlaceholderInHost, 4, 7}}},
		{"placeholder is the whole URL", "{url}", []want{{CodeDestinationNotAbsolute, 0, 5}}},
		{"placeholder in the host of a wrong scheme", "ftp://{host}/x", []want{
			{CodeDestinationScheme, 0, 3}, {CodeDestinationPlaceholderInHost, 6, 12}}},
		{"two placeholders, one in the host", "https://{a}.example.com/{b}", []want{{CodeDestinationPlaceholderInHost, 8, 11}}},
		{"placeholder after a wrong scheme", "ftp://example.com/{x}", []want{{CodeDestinationScheme, 0, 3}}},

		// A placeholder that would finish a percent escape. Go checks
		// escapes in a path and a fragment but not in a query, so the rule
		// is the link model's own, and the same everywhere.
		{"placeholder after a percent sign, path", "https://example.com/%{a}", []want{{CodeDestinationInvalid, 20, 24}}},
		{"placeholder after a percent sign, query", "https://example.com/?q=%{a}", []want{{CodeDestinationInvalid, 23, 27}}},
		{"placeholder after a percent sign, fragment", "https://example.com/#%{a}", []want{{CodeDestinationInvalid, 21, 25}}},
		{"placeholder after half an escape", "https://example.com/?q=%4{a}", []want{{CodeDestinationInvalid, 23, 28}}},
		{"two placeholders after a percent sign", "https://example.com/?q=%{a}{b}", []want{{CodeDestinationInvalid, 23, 27}}},

		// Length.
		{"2049 characters", tooLong, []want{{CodeDestinationTooLong, 2048, 2049}}},
		{"too long and not a URL", strings.Repeat("a", 2050), []want{
			{CodeDestinationNotAbsolute, 0, 2050}, {CodeDestinationTooLong, 2048, 2050}}},
		{"too long with a space before the limit", "https://example.com/a b" + strings.Repeat("a", 2048), []want{
			{CodeDestinationBadCharacter, 21, 22}, {CodeDestinationTooLong, 2048, 2071}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, problems := ParseDestination(tt.dest, noPattern)
			var got []want
			for _, p := range problems {
				if p.Severity != SeverityError {
					t.Errorf("problem %+v is not an error", p)
				}
				got = append(got, want{p.Code, p.Start, p.End})
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ParseDestination(%q) problems =\n  %+v\nwant\n  %+v", tt.dest, problems, tt.want)
			}
			checkProblems(t, tt.dest, FieldDestination, problems)
			if tmpl.String() != "" || tmpl.HasPlaceholders() {
				t.Errorf("ParseDestination(%q) returned a template with errors", tt.dest)
			}
			if _, err := tmpl.Expand(nil); !errors.Is(err, ErrExpand) {
				t.Errorf("the zero Template expanded: %v", err)
			}
		})
	}
}

// Placeholders against the variables of the route's pattern.
func TestParseDestinationVariables(t *testing.T) {
	type want struct {
		severity, code string
		start, end     int
		mentions       string
	}
	tests := []struct {
		name    string
		pattern string
		dest    string
		want    []want
	}{
		{"every variable used once", "reports/{month}/{day}", "https://example.com/daily?month={month}&day={day}", nil},
		{"a variable used twice", "gh/{repo}", "https://example.com/{repo}/tree/{repo}", nil},
		{"path, query and fragment", "x/{a}/{b}/{c}", "https://example.com/{a}?b={b}#{c}", nil},
		{"no variables, no placeholders", "gh", "https://example.com/", nil},
		{"placeholders glued together", "x/{a}/{b}", "https://example.com/{a}{b}", nil},
		{"a placeholder inside a segment", "x/{a}", "https://example.com/v{a}.html", nil},

		{"PRD 5.7: {year} is not declared", "reports/{month}", "https://example.com/{year}", []want{
			{SeverityError, CodeDestinationUndeclaredVariable, 20, 26, "Its only variable is {month}"}}},
		{"undeclared, several declared", "x/{a}/{b}", "https://example.com/{a}/{b}/{c}", []want{
			{SeverityError, CodeDestinationUndeclaredVariable, 28, 31, "Its variables are {a}, {b}"}}},
		{"undeclared, none declared", "gh", "https://example.com/{repo}", []want{
			{SeverityError, CodeDestinationUndeclaredVariable, 20, 26, "it has no variables at all"}}},
		{"names are case-sensitive", "gh/{repo}", "https://example.com/{Repo}", []want{
			{SeverityError, CodeDestinationUndeclaredVariable, 20, 26, "spelled {repo}"}}},
		{"undeclared twice", "gh", "https://example.com/{a}/{a}", []want{
			{SeverityError, CodeDestinationUndeclaredVariable, 20, 23, ""},
			{SeverityError, CodeDestinationUndeclaredVariable, 24, 27, ""}}},
		{"undeclared and in the host", "gh/{repo}", "https://{team}.example.com/{repo}", []want{
			{SeverityError, CodeDestinationPlaceholderInHost, 8, 14, "never in the scheme, the host or the port"},
			{SeverityError, CodeDestinationUndeclaredVariable, 8, 14, "{team}"}}},
		{"declared, but in the host", "wiki/{team}", "https://{team}.example.com/", []want{
			{SeverityError, CodeDestinationPlaceholderInHost, 8, 14, "{team} can't be used here"}}},

		// An unused variable is a warning: the link can be saved (PRD 5.4).
		{"one variable unused", "reports/{month}/{day}", "https://example.com/daily?month={month}", []want{
			{SeverityWarning, CodeDestinationUnusedVariable, 39, 39, "doesn't use {day}"}}},
		{"no variable used", "reports/{month}/{day}", "https://example.com/daily", []want{
			{SeverityWarning, CodeDestinationUnusedVariable, 25, 25, "doesn't use {month}"},
			{SeverityWarning, CodeDestinationUnusedVariable, 25, 25, "doesn't use {day}"}}},
		{"encoded braces are not a placeholder", "gh/{repo}", "https://example.com/%7Brepo%7D", []want{
			{SeverityWarning, CodeDestinationUnusedVariable, 30, 30, "doesn't use {repo}"}}},
		// Next to an error the warning would be noise.
		{"unused is not reported beside an error", "reports/{month}/{day}", "javascript:alert({month})", []want{
			{SeverityError, CodeDestinationScheme, 0, 10, "http:// or https://"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, problems := ParseDestination(tt.dest, mustParse(t, tt.pattern))
			if len(problems) != len(tt.want) {
				t.Fatalf("problems =\n  %+v\nwant %d:\n  %+v", problems, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				p := problems[i]
				if p.Severity != w.severity || p.Code != w.code || p.Start != w.start || p.End != w.end ||
					!strings.Contains(p.Message, w.mentions) {
					t.Errorf("problem %d = %+v\nwant %+v", i, p, w)
				}
			}
			checkProblems(t, tt.dest, FieldDestination, problems)
			// Warnings do not cost the template; errors do.
			if valid := tmpl.String() == tt.dest; valid == HasErrors(problems) {
				t.Errorf("template %q returned with problems %+v", tmpl.String(), problems)
			}
		})
	}
}

// A destination on the Directory itself could loop (PRD 5.4). The link model
// cannot know where the Directory is; the application says.
func TestParseDestinationOwnHosts(t *testing.T) {
	tests := []struct {
		name string
		own  []string
		dest string
		want bool // refused as the Directory
	}{
		{"local Directory", []string{"localhost:4655"}, "http://localhost:4655/-/links", true},
		{"its root", []string{"localhost:4655"}, "http://localhost:4655", true},
		{"its resolver", []string{"localhost:4655"}, "http://localhost:4655/roadmap", true},
		{"with a placeholder", []string{"localhost:4655"}, "http://localhost:4655/{x}", true},
		{"host in upper case", []string{"localhost:4655"}, "http://LOCALHOST:4655/x", true},
		{"entry in upper case", []string{"LocalHost:4655"}, "http://localhost:4655/x", true},
		{"second entry", []string{"localhost:4655", "127.0.0.1:4655"}, "http://127.0.0.1:4655/x", true},
		{"IPv6 loopback", []string{"[::1]:4655"}, "http://[::1]:4655/x", true},
		{"another port of localhost is another site", []string{"localhost:4655"}, "http://localhost:3000/app", false},
		{"localhost without a port is port 80", []string{"localhost:4655"}, "http://localhost/app", false},
		{"another loopback spelling is not assumed", []string{"localhost:4655"}, "http://127.0.0.1:4655/x", false},
		{"another host", []string{"localhost:4655"}, "https://example.com/", false},
		{"nothing configured", nil, "http://localhost:4655/x", false},

		{"deployed, bare host", []string{"go.example.com"}, "https://go.example.com/x", true},
		{"bare host is the default port of either scheme", []string{"go.example.com"}, "http://go.example.com/x", true},
		{"explicit default port", []string{"go.example.com"}, "https://go.example.com:443/x", true},
		{"bare host, another port", []string{"go.example.com"}, "https://go.example.com:8443/x", false},
		{"fully qualified", []string{"go.example.com"}, "https://go.example.com./x", true},
		{"a subdomain is another host", []string{"go.example.com"}, "https://docs.go.example.com/x", false},
		{"a suffix is another host", []string{"go.example.com"}, "https://go.example.com.evil.example/x", false},

		{"base URL", []string{"https://go.example.com"}, "https://go.example.com/x", true},
		{"base URL, explicit port in the destination", []string{"https://go.example.com"}, "https://go.example.com:443/x", true},
		{"base URL fixes the port", []string{"https://go.example.com"}, "http://go.example.com/x", false},
		{"base URL with a port and a path", []string{"http://localhost:4655/-/"}, "http://localhost:4655/x", true},
		{"entries that cannot be read are ignored", []string{"", "://", "http://[::1", "a b"}, "https://example.com/", false},

		// A browser rewrites every one of these to the address on the left
		// before it connects, so each is the Directory as much as the plain
		// spelling is.
		{"IPv4 with a part left out", []string{"127.0.0.1:4655"}, "http://127.1:4655/gh", true},
		{"IPv4 with two parts left out", []string{"127.0.0.1:4655"}, "http://127.0.1:4655/gh", true},
		{"IPv4 as one number", []string{"127.0.0.1:4655"}, "http://2130706433:4655/gh", true},
		{"IPv4 with a hexadecimal part", []string{"127.0.0.1:4655"}, "http://0x7f.0.0.1:4655/gh", true},
		{"IPv4 as one hexadecimal number", []string{"127.0.0.1:4655"}, "http://0X7F000001:4655/gh", true},
		{"IPv4 as one octal number", []string{"127.0.0.1:4655"}, "http://017700000001:4655/gh", true},
		{"IPv4 with octal parts", []string{"127.0.0.1:4655"}, "http://0177.0.0.01:4655/gh", true},
		{"IPv4 with a trailing dot", []string{"127.0.0.1:4655"}, "http://127.0.0.1.:4655/gh", true},
		{"a port with leading zeros", []string{"localhost:4655"}, "http://localhost:04655/gh", true},
		{"a default port with leading zeros", []string{"go.example.com"}, "http://go.example.com:0080/gh", true},
		{"IPv6 written out", []string{"[::1]:4655"}, "http://[0:0:0:0:0:0:0:1]:4655/gh", true},
		{"IPv6 with leading zeros", []string{"[::1]:4655"}, "http://[0000::0001]:4655/gh", true},
		{"IPv6 in upper case", []string{"[2001:db8::a]:4655"}, "http://[2001:DB8:0::A]:4655/gh", true},
		{"IPv4 inside IPv6", []string{"127.0.0.1:4655"}, "http://[::ffff:127.0.0.1]:4655/gh", true},
		{"the entry is the unusual spelling", []string{"127.1:04655"}, "http://127.0.0.1:4655/gh", true},
		{"another address, one number", []string{"127.0.0.1:4655"}, "http://2130706434:4655/gh", false},
		{"another address, part left out", []string{"127.0.0.1:4655"}, "http://127.2:4655/gh", false},
		{"another port, leading zeros", []string{"localhost:4655"}, "http://localhost:04656/gh", false},
		{"a name that only looks numeric", []string{"127.0.0.1:4655"}, "http://127.0.0.1.example.com:4655/gh", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, problems := ParseDestination(tt.dest, noPattern, ValidateOptions{OwnHosts: tt.own})
			refused := len(problems) == 1 && problems[0].Code == CodeDestinationDirectory
			if refused != tt.want || (!tt.want && len(problems) != 0) {
				t.Errorf("ParseDestination(%q) with own hosts %q: problems %+v, want refused = %v", tt.dest, tt.own, problems, tt.want)
			}
			checkProblems(t, tt.dest, FieldDestination, problems)
		})
	}
}

func TestTemplateInPath(t *testing.T) {
	pattern := mustParse(t, "x/{a}/{b}/{c}/{d}")
	tests := []struct {
		dest string
		want string // the variables for which InPath is true
	}{
		{"https://example.com/{a}/{b}/{c}/{d}", "abcd"},
		{"https://example.com/{a}?b={b}&c={c}#{d}", "a"},
		{"https://example.com/?a={a}&b={b}&c={c}&d={d}", ""},
		{"https://example.com/#{a}{b}{c}{d}", ""},
		{"https://example.com?x={a}#{b}/{c}?{d}", ""}, // "/" and "?" after "#" are still the fragment
		{"https://example.com/x?a={a}/{b}#{c}", ""},   // "/" after "?" is still the query
		{"https://example.com/{a}?a={a}#{a}{b}", "a"}, // once in the path is enough
		{"https://example.com/p/{b}.html?q={a}&r={c}&s={d}", "b"},
		{"https://example.com/{a}{b}/x{c}y?d={d}", "abc"},
	}
	for _, tt := range tests {
		tmpl, problems := ParseDestination(tt.dest, pattern)
		if HasErrors(problems) {
			t.Fatalf("ParseDestination(%q) problems = %+v", tt.dest, problems)
		}
		got := ""
		for _, v := range []string{"a", "b", "c", "d", "zzz"} {
			if tmpl.InPath(v) {
				got += v
			}
		}
		if got != tt.want {
			t.Errorf("%q: InPath is true for %q, want %q", tt.dest, got, tt.want)
		}
	}
	if (Template{}).InPath("a") {
		t.Error("the zero Template says a variable is in its path")
	}
}

func TestTemplateExpand(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		dest    string
		values  map[string]string
		want    string
	}{
		{"no placeholders", "gh", "https://github.com/acme", nil, "https://github.com/acme"},
		{"no placeholders, values ignored", "gh/{x}", "https://github.com/acme", map[string]string{"x": "1"}, "https://github.com/acme"},
		{"path", "gh/{repo}", "https://github.com/acme/{repo}", map[string]string{"repo": "api-server"}, "https://github.com/acme/api-server"},
		{"two in the path", "gh/{repo}/pr/{num}", "https://github.com/acme/{repo}/pull/{num}",
			map[string]string{"repo": "api-server", "num": "42"}, "https://github.com/acme/api-server/pull/42"},
		{"query", "reports/{month}/{day}", "https://reports.example.com/daily?month={month}&day={day}",
			map[string]string{"month": "10", "day": "13"}, "https://reports.example.com/daily?month=10&day=13"},
		{"values are kept as typed", "reports/{month}", "https://example.com/?m={month}", map[string]string{"month": "03"}, "https://example.com/?m=03"},
		{"the same variable twice", "x/{v}", "https://example.com/{v}/and/{v}?again={v}", map[string]string{"v": "a b"},
			"https://example.com/a%20b/and/a%20b?again=a%20b"},
		{"fragment", "x/{v}", "https://example.com/doc#{v}", map[string]string{"v": "a b#c"}, "https://example.com/doc#a%20b%23c"},
		{"existing query", "x/{v}", "https://example.com/s?lang=en&q={v}&page=1", map[string]string{"v": "a&page=9"},
			"https://example.com/s?lang=en&q=a%26page%3D9&page=1"},
		{"literal encoded braces stay", "x/{v}", "https://example.com/%7B{v}%7D", map[string]string{"v": "a"}, "https://example.com/%7Ba%7D"},
		{"case of the template is kept", "x/{v}", "HTTPS://Example.COM/Path/{v}", map[string]string{"v": "A"}, "HTTPS://Example.COM/Path/A"},
		{"non-ASCII template text is kept", "x/{v}", "https://example.com/żółć/{v}", map[string]string{"v": "ż"}, "https://example.com/żółć/%C5%BC"},
		{"port and IPv6", "x/{v}", "http://[::1]:9000/{v}", map[string]string{"v": "a"}, "http://[::1]:9000/a"},

		// PRD 5.7, row 14.
		{"decoded value is re-encoded", "search/{q}", "https://search.example.com/?q={q}", map[string]string{"q": "a b&c"},
			"https://search.example.com/?q=a%20b%26c"},

		// Only unreserved characters stay as they are; nothing a value
		// contains can add structure to the URL.
		{"unreserved characters", "x/{v}", "https://example.com/{v}", map[string]string{"v": "AZaz09-._~"}, "https://example.com/AZaz09-._~"},
		{"slash", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a/b"}, "https://example.com/a%2Fb"},
		{"climbing", "x/{v}", "https://example.com/a/{v}", map[string]string{"v": "../../etc"}, "https://example.com/a/..%2F..%2Fetc"},
		{"backslash", "x/{v}", "https://example.com/{v}", map[string]string{"v": `..\..\x`}, "https://example.com/..%5C..%5Cx"},
		{"question mark", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a?admin=1"}, "https://example.com/a%3Fadmin%3D1"},
		{"hash", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a#frag"}, "https://example.com/a%23frag"},
		{"ampersand and equals", "x/{v}", "https://example.com/?q={v}", map[string]string{"v": "a&b=c"}, "https://example.com/?q=a%26b%3Dc"},
		{"semicolon", "x/{v}", "https://example.com/?q={v}", map[string]string{"v": "a;b"}, "https://example.com/?q=a%3Bb"},
		{"percent", "x/{v}", "https://example.com/{v}", map[string]string{"v": "100%"}, "https://example.com/100%25"},
		{"an escape in a value is data", "x/{v}", "https://example.com/{v}", map[string]string{"v": "%2F%41"}, "https://example.com/%252F%2541"},
		{"space", "x/{v}", "https://example.com/?q={v}", map[string]string{"v": "a b"}, "https://example.com/?q=a%20b"},
		{"plus", "x/{v}", "https://example.com/?q={v}", map[string]string{"v": "a+b"}, "https://example.com/?q=a%2Bb"},
		{"at sign and colon", "x/{v}", "https://example.com/{v}", map[string]string{"v": "u:p@evil.example"}, "https://example.com/u%3Ap%40evil.example"},
		{"a whole URL", "x/{v}", "https://example.com/r?to={v}", map[string]string{"v": "https://evil.example/?a=b#c"},
			"https://example.com/r?to=https%3A%2F%2Fevil.example%2F%3Fa%3Db%23c"},
		{"line break", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a\r\nSet-Cookie: x=1"},
			"https://example.com/a%0D%0ASet-Cookie%3A%20x%3D1"},
		{"NUL and DEL", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a\x00b\x7f"}, "https://example.com/a%00b%7F"},
		{"tab", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a\tb"}, "https://example.com/a%09b"},
		{"braces", "x/{v}", "https://example.com/{v}", map[string]string{"v": "{v}"}, "https://example.com/%7Bv%7D"},
		{"quotes and angle brackets", "x/{v}", "https://example.com/{v}", map[string]string{"v": `"<script>'`}, "https://example.com/%22%3Cscript%3E%27"},
		{"unicode", "x/{v}", "https://example.com/{v}", map[string]string{"v": "żółć 日本"}, "https://example.com/%C5%BC%C3%B3%C5%82%C4%87%20%E6%97%A5%E6%9C%AC"},
		{"emoji", "x/{v}", "https://example.com/{v}", map[string]string{"v": "👍"}, "https://example.com/%F0%9F%91%8D"},
		{"right-to-left override", "x/{v}", "https://example.com/{v}", map[string]string{"v": "\u202egpj"}, "https://example.com/%E2%80%AEgpj"},
		{"bytes that are not UTF-8", "x/{v}", "https://example.com/{v}", map[string]string{"v": "a\xff"}, "https://example.com/a%FF"},
		// "." and ".." are unreserved and pass through here. Matching refuses
		// them for a variable in the path (NFR-10); see TestMatchDotSegments.
		{"dots pass through expansion", "x/{v}", "https://example.com/?q={v}", map[string]string{"v": ".."}, "https://example.com/?q=.."},
		{"empty value", "x/{v}", "https://example.com/a/{v}/b", map[string]string{"v": ""}, "https://example.com/a//b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, problems := ParseDestination(tt.dest, mustParse(t, tt.pattern))
			if HasErrors(problems) {
				t.Fatalf("ParseDestination(%q) problems = %+v", tt.dest, problems)
			}
			got, err := tmpl.Expand(tt.values)
			if err != nil || got != tt.want {
				t.Fatalf("Expand(%q) = %q, %v\nwant %q", tt.values, got, err, tt.want)
			}
			checkExpanded(t, tt.dest, got)
		})
	}
}

// checkExpanded asserts what must hold for every expansion of a valid
// template: it parses as a URL on the template's scheme and host, with no
// credentials, and carries nothing that could break out of a header.
func checkExpanded(t testing.TB, dest, expanded string) {
	t.Helper()
	want, err := url.Parse(probe(dest, mustPlaceholders(t, dest)))
	if err != nil {
		t.Fatalf("the template %q does not parse: %v", dest, err)
	}
	got, err := url.Parse(expanded)
	if err != nil {
		t.Fatalf("expansion %q of %q does not parse: %v", expanded, dest, err)
	}
	if got.Scheme != want.Scheme || got.Host != want.Host || got.User != nil {
		t.Fatalf("expansion %q of %q is on %s://%s (user %v), want %s://%s",
			expanded, dest, got.Scheme, got.Host, got.User, want.Scheme, want.Host)
	}
	if strings.ContainsAny(expanded, " \t\r\n\x00{}") {
		t.Fatalf("expansion %q of %q contains a raw space, line break, NUL or brace", expanded, dest)
	}
	if start, end, bad := firstBadCharacter(expanded); bad {
		t.Fatalf("expansion %q of %q contains the forbidden character %q", expanded, dest, expanded[start:end])
	}
}

func mustPlaceholders(t testing.TB, dest string) []placeholder {
	t.Helper()
	found, ok := scanPlaceholders(dest, func(string, string, int, int) {})
	if !ok {
		t.Fatalf("the template %q has stray braces", dest)
	}
	return found
}

func TestTemplateExpandErrors(t *testing.T) {
	pattern := mustParse(t, "x/{a}/{b}")
	tmpl, problems := ParseDestination("https://example.com/{a}?b={b}", pattern)
	if problems != nil {
		t.Fatal(problems)
	}
	for name, values := range map[string]map[string]string{
		"nil":            nil,
		"empty":          {},
		"one missing":    {"a": "1"},
		"other names":    {"A": "1", "B": "2"},
		"secret missing": {"b": "secret-value"},
	} {
		got, err := tmpl.Expand(values)
		if got != "" || !errors.Is(err, ErrExpand) {
			t.Errorf("%s: Expand = %q, %v; want an ErrExpand", name, got, err)
		}
		// NFR-17: an error is logged; a value must not be in it.
		if err != nil && strings.Contains(err.Error(), "secret-value") {
			t.Errorf("%s: the error %q contains a value", name, err)
		}
	}
	if got, err := (Template{}).Expand(map[string]string{"a": "1"}); got != "" || !errors.Is(err, ErrExpand) {
		t.Errorf("the zero Template expanded to %q, %v", got, err)
	}
}

// The assertion in Expand is the second line of defence: validation keeps a
// placeholder out of the host, and should it ever fail to, Expand refuses to
// produce a URL on another host. The templates here are built by hand,
// because ParseDestination will not make them.
func TestTemplateExpandAssertsSchemeAndHost(t *testing.T) {
	lit := func(s string) templatePart { return templatePart{literal: s} }
	v := func(s string) templatePart { return templatePart{variable: s} }
	tests := []struct {
		name   string
		tmpl   Template
		value  string
		wantOK bool
	}{
		{"placeholder is the host", Template{parts: []templatePart{lit("https://"), v("v"), lit("/x")}, scheme: "https", host: "x"}, "evil.example", false},
		{"placeholder is the host, same value", Template{parts: []templatePart{lit("https://"), v("v"), lit("/x")}, scheme: "https", host: "x"}, "x", true},
		{"placeholder ends the host", Template{parts: []templatePart{lit("https://example.com"), v("v")}, scheme: "https", host: "example.comx"}, ".evil.example", false},
		{"placeholder is the scheme", Template{parts: []templatePart{v("v"), lit("://example.com/")}, scheme: "https", host: "example.com"}, "http", false},
		{"placeholder is the port", Template{parts: []templatePart{lit("https://example.com:"), v("v"), lit("/")}, scheme: "https", host: "example.com:x"}, "8080", false},
		{"recorded host is wrong", Template{parts: []templatePart{lit("https://example.com/"), v("v")}, scheme: "https", host: "other.example"}, "a", false},
		{"recorded scheme is wrong", Template{parts: []templatePart{lit("https://example.com/"), v("v")}, scheme: "http", host: "example.com"}, "a", false},
		{"nothing recorded", Template{parts: []templatePart{lit("https://example.com/"), v("v")}}, "a", false},
		{"as ParseDestination builds it", Template{parts: []templatePart{lit("https://example.com/"), v("v")}, scheme: "https", host: "example.com"}, "a/../b", true},
	}
	for _, tt := range tests {
		got, err := tt.tmpl.Expand(map[string]string{"v": tt.value})
		if ok := err == nil; ok != tt.wantOK {
			t.Errorf("%s: Expand = %q, %v; want ok = %v", tt.name, got, err, tt.wantOK)
		}
		if err != nil && (got != "" || !errors.Is(err, ErrExpand)) {
			t.Errorf("%s: Expand = %q, %v; want no URL and an ErrExpand", tt.name, got, err)
		}
		if err != nil && strings.Contains(err.Error(), "evil.example") {
			t.Errorf("%s: the error %q contains the value", tt.name, err)
		}
	}
}

// A route read back from the store is not validated again: a destination
// saved before a rule existed keeps working. Only what expansion depends on
// is required.
func TestStoredTemplate(t *testing.T) {
	pattern := mustParse(t, "x/{v}")
	tests := []struct {
		name   string
		dest   string
		value  string
		want   string // expansion; "" when the route cannot be used
		wantOK bool
	}{
		{"valid", "https://example.com/{v}", "a b", "https://example.com/a%20b", true},
		{"static with credentials, saved by the first release", "https://user:secret@example.com/", "", "https://user:secret@example.com/", true},
		{"static go link", "http://go/other", "", "http://go/other", true},
		{"static and not a URL at all", "not a url", "", "not a url", true},
		{"static with another scheme", "ftp://example.com/x", "", "ftp://example.com/x", true},
		{"templated with credentials", "https://user:secret@example.com/{v}", "a/b", "https://user:secret@example.com/a%2Fb", true},
		{"templated go link", "http://go/{v}", "a", "http://go/a", true},
		{"templated and over the length limit", "https://example.com/" + strings.Repeat("a", MaxDestinationLength) + "/{v}", "a",
			"https://example.com/" + strings.Repeat("a", MaxDestinationLength) + "/a", true},

		{"empty", "", "", "", false},
		{"undeclared variable", "https://example.com/{other}", "", "", false},
		{"placeholder in the host", "https://{v}.example.com/", "", "", false},
		{"placeholder in the host, with credentials", "https://user@{v}/", "", "", false},
		{"unclosed brace", "https://example.com/{v", "", "", false},
		{"stray brace", "https://example.com/v}", "", "", false},
		{"templated with another scheme", "ftp://example.com/{v}", "", "", false},
		{"templated and unparseable", "https://example.com:port/{v}", "", "", false},
		{"templated with a line break", "https://example.com/\n{v}", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, ok := storedTemplate(tt.dest, pattern)
			if ok != tt.wantOK {
				t.Fatalf("storedTemplate(%q) ok = %v, want %v", tt.dest, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got, err := tmpl.Expand(map[string]string{"v": tt.value}); err != nil || got != tt.want {
				t.Errorf("Expand = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestHasErrors(t *testing.T) {
	warning := Problem{Severity: SeverityWarning}
	failure := Problem{Severity: SeverityError}
	for _, tt := range []struct {
		problems []Problem
		want     bool
	}{
		{nil, false},
		{[]Problem{warning}, false},
		{[]Problem{warning, warning}, false},
		{[]Problem{failure}, true},
		{[]Problem{warning, failure}, true},
		// A severity this version does not know blocks: fail closed.
		{[]Problem{{Severity: ""}}, true},
	} {
		if got := HasErrors(tt.problems); got != tt.want {
			t.Errorf("HasErrors(%+v) = %v, want %v", tt.problems, got, tt.want)
		}
	}
}

// A megabyte that is not a destination is refused quickly and briefly.
func TestParseDestinationBoundsItsWork(t *testing.T) {
	const size = 1 << 20
	pattern := mustParse(t, "x/{a}")
	for name, text := range map[string]string{
		"opening braces": "https://example.com/" + strings.Repeat("{", size),
		"closing braces": "https://example.com/" + strings.Repeat("}", size),
		"placeholders":   "https://example.com/" + strings.Repeat("{b}", size/3),
		"letters":        "https://example.com/" + strings.Repeat("a", size),
		"at signs":       "https://" + strings.Repeat("@", size),
		"not a URL":      strings.Repeat("a", size),
	} {
		tmpl, problems := ParseDestination(text, pattern)
		if tmpl.String() != "" || len(problems) != 1 || problems[0].Code != CodeDestinationTooLong {
			t.Errorf("%s: %d problems, first %+v; want only %s", name, len(problems), problems[0], CodeDestinationTooLong)
		}
		checkProblems(t, text, FieldDestination, problems)
	}
	// Within the length that is read, many mistakes are still few problems,
	// and a "{" is searched for its "}" once, not once per brace.
	for name, text := range map[string]string{
		"opening braces": "https://example.com/" + strings.Repeat("{", 8000),
		"closing braces": "https://example.com/" + strings.Repeat("}", 8000),
		"undeclared":     "https://example.com/" + strings.Repeat("{b}", 2600),
		"bad":            "https://example.com/" + strings.Repeat("{}", 4000),
	} {
		_, problems := ParseDestination(text, pattern)
		if len(problems) == 0 || len(problems) > maxDestinationProblems || !HasErrors(problems) {
			t.Errorf("%s: %d problems, want 1 to %d errors", name, len(problems), maxDestinationProblems)
		}
		checkProblems(t, text, FieldDestination, problems)
	}
}

// The spellings of an IPv4 address that a browser accepts, and the text that
// only resembles one (WHATWG URL, "IPv4 parser").
func TestBrowserIPv4(t *testing.T) {
	tests := []struct {
		host string
		want string // "" when the host is not an IPv4 address
	}{
		{"127.0.0.1", "127.0.0.1"},
		{"127.1", "127.0.0.1"},
		{"127.0.1", "127.0.0.1"},
		{"1.2.772", "1.2.3.4"},
		{"2130706433", "127.0.0.1"},
		{"0x7f000001", "127.0.0.1"},
		{"0x7f.1", "127.0.0.1"},
		{"017700000001", "127.0.0.1"},
		{"0177.0.0.1", "127.0.0.1"},
		{"0", "0.0.0.0"},
		{"0x", "0.0.0.0"},
		{"4294967295", "255.255.255.255"},
		{"255.255.255.255", "255.255.255.255"},
		{"4294967296", ""},  // one more than fits
		{"256.0.0.1", ""},   // a part over one byte
		{"1.2.3.256", ""},   // the last of four parts is one byte too
		{"1.2.65536", ""},   // the last of three parts is two bytes
		{"1.16777216", ""},  // the last of two parts is three bytes
		{"1.2.3.4.5", ""},   // too many parts
		{"08", ""},          // not octal
		{"0xg", ""},         // not hexadecimal
		{"1..2", ""},        // an empty part
		{"", ""},            // nothing
		{"+1", ""},          // no signs
		{"1_0", ""},         // no digit separators
		{"localhost", ""},   // a name
		{"1e3", ""},         // no exponents
		{"127.0.0.1x", ""},  // a name that begins like an address
		{"99999999999", ""}, // more than 32 bits
	}
	for _, tt := range tests {
		got := ""
		if addr, ok := browserIPv4(tt.host); ok {
			got = fmt.Sprintf("%d.%d.%d.%d", addr[0], addr[1], addr[2], addr[3])
		}
		if got != tt.want {
			t.Errorf("browserIPv4(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}
