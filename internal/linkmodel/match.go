package linkmodel

// CompiledLink is a link ready for matching: every route's pattern is parsed
// once, when the link enters the index, and not again for each request. It is
// immutable after Compile.
type CompiledLink struct {
	// Link is the link as stored. Its Routes are shared with the compiled
	// form: read them, never modify them.
	Link     Link
	patterns []Pattern // patterns[i] belongs to Link.Routes[i]
}

// Compile prepares l for matching.
//
// A route whose saved pattern no longer parses is kept in Link.Routes but
// never matches. Every pattern is validated before it is saved, so this only
// concerns data written by something other than godir; refusing to start
// over one such route would take every other link down with it.
func Compile(l Link) CompiledLink {
	c := CompiledLink{Link: l, patterns: make([]Pattern, len(l.Routes))}
	for i, r := range l.Routes {
		// The problems are dropped on purpose: a failed parse leaves the zero
		// Pattern, which matches nothing.
		c.patterns[i], _ = ParsePattern(r.Pattern)
	}
	return c
}

// Match returns the route that answers the request segments (as returned by
// SplitPath), or false when none of the link's routes fits.
//
// L1 first cut: patterns are literal-only and unique within a link, so at
// most one route can match and the first match is the answer. L2 adds
// choosing the most specific of several (PRD 5.5, step 4). The choice lives
// here, not in the resolver, so that the rules of PRD section 5 have a single
// implementation (NFR-22).
func (c CompiledLink) Match(segments []string) (Route, bool) {
	for i, p := range c.patterns {
		if p.Matches(segments) {
			return c.Link.Routes[i], true
		}
	}
	return Route{}, false
}
