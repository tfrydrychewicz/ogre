package linkmodel

import (
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"roadmap", "roadmap"},
		{"Roadmap", "roadmap"},
		{"ROADMAP", "roadmap"},
		{"On-Call", "on-call"},
		{"On_Call", "on_call"},
		{"OnCall", "oncall"},
		{"V1.2", "v1.2"},
		{"a1B2", "a1b2"},
		// Only case is ignored: nothing is trimmed, removed or mapped.
		{" Roadmap ", " roadmap "},
		{"a b", "a b"},
		// ASCII folding only: these are not names and must not become one.
		{"K", "K"}, // KELVIN SIGN; Unicode lowercasing would give "k"
		{"Été", "Été"},
		{"\xff", "\xff"},
	}
	for _, tt := range tests {
		if got := NormalizeName(tt.in); got != tt.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Decision Q5: "-" and "_" are part of the name. The three spellings must
// stay three different names after normalization.
func TestNormalizeNameKeepsSeparators(t *testing.T) {
	names := []string{"on-call", "on_call", "oncall", "on.call"}
	seen := map[string]string{}
	for _, n := range names {
		norm := NormalizeName(n)
		if norm != n {
			t.Errorf("NormalizeName(%q) = %q, want it unchanged", n, norm)
		}
		if other, dup := seen[norm]; dup {
			t.Errorf("%q and %q normalize to the same name %q", n, other, norm)
		}
		seen[norm] = n
	}
}

func TestNormalizeNameIsIdempotent(t *testing.T) {
	for _, in := range []string{"On-Call", "GH", "a_B.c", "K", ""} {
		once := NormalizeName(in)
		if twice := NormalizeName(once); twice != once {
			t.Errorf("NormalizeName(NormalizeName(%q)) = %q, want %q", in, twice, once)
		}
	}
}

func TestEqualFoldASCII(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"", "", true},
		{"gh", "gh", true},
		{"gh", "GH", true},
		{"On-Call", "on-call", true},
		{"on-call", "on_call", false},
		{"on-call", "oncall", false},
		{"gh", "ghx", false},
		{"k", "K", false},
		{"@", "`", false}, // 0x40 and 0x60 differ by the case bit but are not letters
		{"[", "{", false},
	}
	for _, tt := range tests {
		if got := equalFoldASCII(tt.a, tt.b); got != tt.want {
			t.Errorf("equalFoldASCII(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := equalFoldASCII(tt.b, tt.a); got != tt.want {
			t.Errorf("equalFoldASCII(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
		}
	}
}

func TestReservedName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"favicon.ico", true},
		{"Favicon.ICO", true},
		{"robots.txt", true},
		{"sitemap.xml", true},
		{"apple-touch-icon.png", true},
		{"apple-touch-icon-precomposed.png", true},
		{"apple-touch-icon-120x120.png", true},
		{"Apple-Touch-Icon-120x120-precomposed.PNG", true},
		{".well-known", true},

		{"", false},
		{"favicon", false},
		{"favicon.ico.bak", false},
		{"robots", false},
		{"sitemap", false},
		{"apple-touch-icon", false},
		{"apple-touch-icon.jpg", false},
		{"my-apple-touch-icon.png", false},
		{"well-known", false},
		{"roadmap", false},
		{"-", false},
	}
	for _, tt := range tests {
		if got := ReservedName(tt.name); got != tt.want {
			t.Errorf("ReservedName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPossibleName(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"a name", "roadmap", true},
		{"upper case", "RoadMap", true},
		{"separators", "on-call_v1.2", true},
		{"a digit first", "2fa", true},
		{"single character", "a", true},
		{"the longest name", strings.Repeat("a", MaxPatternLength), true},
		// Possible as text; whether it may be saved is ParsePattern's call.
		{"a reserved name", "favicon.ico", true},

		{"empty", "", false},
		{"one character too long", strings.Repeat("a", MaxPatternLength+1), false},
		{"leading hyphen", "-x", false},
		{"leading dot", ".x", false},
		{"dot dot", "..", false},
		{"a decoded slash", "secret-name/secret-value", false},
		{"a space", "a b", false},
		{"a line break", "x\r\nSet-Cookie: a=b", false},
		{"NUL", "a\x00b", false},
		{"markup", "<script>alert(1)</script>", false},
		{"a brace", "{name}", false},
		{"percent", "a%2Fb", false},
		{"non-ASCII letter", "café", false},
		{"non-ASCII first", "éa", false},
		{"kelvin sign", "\u212a", false},
		{"invalid UTF-8", "a\xffb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PossibleName(tt.s); got != tt.want {
				t.Errorf("PossibleName(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
