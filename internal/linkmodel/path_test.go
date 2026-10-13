package linkmodel

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"root", "/", nil},
		{"only slashes", "///", nil},
		{"one segment, leading slash", "/roadmap", []string{"roadmap"}},
		{"one segment, no leading slash", "roadmap", []string{"roadmap"}},
		{"several segments", "/gh/new", []string{"gh", "new"}},
		{"no leading slash, several", "gh/acme/pr/12", []string{"gh", "acme", "pr", "12"}},
		{"trailing slash", "/gh/new/", []string{"gh", "new"}},
		{"double slash at the start", "//double", []string{"double"}},
		{"double slash inside", "/a//b", []string{"a", "b"}},
		{"many empty segments", "//a///b//", []string{"a", "b"}},

		// Case and separators are untouched: folding is the matcher's job.
		{"case is kept", "/On-Call/README", []string{"On-Call", "README"}},
		{"separators are kept", "/on-call/on_call/on.call", []string{"on-call", "on_call", "on.call"}},

		// Decoding happens once, after splitting.
		{"encoded space", "/a%20b", []string{"a b"}},
		{"encoded slash stays inside its segment", "/a%2Fb/c", []string{"a/b", "c"}},
		{"lowercase hex digits", "/a%2fb", []string{"a/b"}},
		{"spike check 9", "/a%20b%2Fc", []string{"a b/c"}},
		{"decoded once only", "/%2541", []string{"%41"}},
		{"double-encoded slash", "/a%252Fb", []string{"a%2Fb"}},
		{"plus is not a space", "/a+b", []string{"a+b"}},
		{"encoded question mark and hash", "/a%3Fb%23c", []string{"a?b#c"}},
		{"encoded UTF-8", "/caf%C3%A9", []string{"café"}},
		{"raw UTF-8", "/café", []string{"café"}},
		{"encoded NUL is an ordinary value", "/a%00b", []string{"a\x00b"}},

		// Query and fragment are not part of the path.
		{"query", "/gh/new?tab=repos", []string{"gh", "new"}},
		{"fragment", "/gh/new#top", []string{"gh", "new"}},
		{"query then fragment", "/gh?x=1#y", []string{"gh"}},
		{"fragment then question mark", "/gh#y?x=1", []string{"gh"}},
		{"only a query", "?x=1", nil},
		{"root with a query", "/?x=1", nil},
		{"slashes in the query are not segments", "/gh?next=/a/b", []string{"gh"}},
		{"a bad escape in the query is ignored", "/gh?x=%zz", []string{"gh"}},

		// No path cleaning: dot segments are ordinary segments.
		{"dot", "/a/./b", []string{"a", ".", "b"}},
		{"dot dot", "/a/../b", []string{"a", "..", "b"}},
		{"leading dot dot", "../etc/passwd", []string{"..", "etc", "passwd"}},
		{"encoded dots", "/%2e%2e/a", []string{"..", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitPath(tt.in)
			if err != nil {
				t.Fatalf("SplitPath(%q) error: %v", tt.in, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("SplitPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitPathErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"non-hex escape", "%zz"},
		{"non-hex escape after a slash", "/a%zzb"},
		{"lone percent", "%"},
		{"lone percent after a slash", "/%"},
		{"truncated escape", "/a%2"},
		{"escape cut by a slash", "/a%2/Fb"},
		{"bad escape in a later segment", "/gh/%G0"},
		{"invalid UTF-8 byte", "/%ff"},
		{"invalid UTF-8 after a valid name", "/gh/%ff"},
		{"truncated UTF-8 sequence", "/%C3"},
		{"overlong encoding", "/%C0%AF"},
		{"surrogate half", "/%ED%A0%80"},
		{"raw invalid byte", "/a\xffb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitPath(tt.in)
			if err == nil {
				t.Fatalf("SplitPath(%q) = %q, want an error", tt.in, got)
			}
			if !errors.Is(err, ErrBadPath) {
				t.Errorf("SplitPath(%q) error = %v, want one wrapping ErrBadPath", tt.in, err)
			}
			if got != nil {
				t.Errorf("SplitPath(%q) returned segments %q with an error", tt.in, got)
			}
			// NFR-17: the raw path must not leak into logs through the error.
			if len(tt.in) > 2 && strings.Contains(err.Error(), tt.in) {
				t.Errorf("error %q contains the raw path %q", err, tt.in)
			}
		})
	}
}

func FuzzSplitPath(f *testing.F) {
	for _, seed := range []string{
		"", "/", "roadmap", "/gh/new", "//double", "/a//b/", "/a%20b%2Fc",
		"%zz", "%", "/%ff", "/a%2", "/gh?x=1#y", "/a/../b", "/%2541", "/caf%C3%A9",
		"/a\xffb", "/%00", "#", "?", "/a+b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		segments, err := SplitPath(in)
		if err != nil {
			if !errors.Is(err, ErrBadPath) {
				t.Fatalf("SplitPath(%q) error %v does not wrap ErrBadPath", in, err)
			}
			if segments != nil {
				t.Fatalf("SplitPath(%q) returned segments %q with an error", in, segments)
			}
			return
		}
		path, _, _ := strings.Cut(in, "?")
		path, _, _ = strings.Cut(path, "#")
		if max := strings.Count(path, "/") + 1; len(segments) > max {
			t.Fatalf("SplitPath(%q) returned %d segments from a path with room for %d", in, len(segments), max)
		}
		for i, seg := range segments {
			if seg == "" {
				t.Fatalf("SplitPath(%q) segment %d is empty", in, i)
			}
			if !utf8.ValidString(seg) {
				t.Fatalf("SplitPath(%q) segment %d = %q is not valid UTF-8", in, i, seg)
			}
		}
	})
}
