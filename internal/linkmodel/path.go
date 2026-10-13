package linkmodel

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ErrBadPath is returned (wrapped) by SplitPath for a request path that
// cannot be decoded. The server answers such a request with 400 rather than
// a miss, because there is no name to look up or suggest corrections for.
var ErrBadPath = errors.New("bad request path")

// SplitPath cuts a request path into its decoded segments (PRD 5.5, step 1).
//
// The input is the raw, still percent-encoded path, with or without a leading
// slash. A query string or fragment, if present, is ignored. Empty segments
// are discarded, so "/a//b/" is the same request as "a/b".
//
// Each segment is percent-decoded exactly once and only after splitting. That
// order is the point of the function: an encoded slash (%2F) stays inside its
// segment instead of creating a new one, and "%2541" yields "%41", not "A".
// "+" is not a space in a path and is kept as is.
//
// The segments "." and ".." are returned like any other; no path cleaning is
// done here. They never equal a literal, so matching simply does not find
// them.
//
// A malformed escape or a segment that is not valid UTF-8 after decoding
// yields an error wrapping ErrBadPath. The error text never contains the path
// (NFR-17: raw paths are not logged).
func SplitPath(escapedPath string) ([]string, error) {
	if i := strings.IndexAny(escapedPath, "?#"); i >= 0 {
		escapedPath = escapedPath[:i]
	}
	// One allocation for the usual request. The capacity is capped because a
	// path of nothing but slashes would otherwise reserve memory for segments
	// that do not exist; a longer path than any pattern just grows the slice.
	segments := make([]string, 0, min(strings.Count(escapedPath, "/")+1, MaxPatternSegments))
	for len(escapedPath) > 0 {
		raw, rest, _ := strings.Cut(escapedPath, "/")
		escapedPath = rest
		if raw == "" {
			continue
		}
		seg, err := url.PathUnescape(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: malformed percent-escape", ErrBadPath)
		}
		if !utf8.ValidString(seg) {
			return nil, fmt.Errorf("%w: segment is not valid UTF-8", ErrBadPath)
		}
		segments = append(segments, seg)
	}
	return segments, nil
}
