package app

import (
	"net/url"
	"slices"
	"strings"

	"github.com/tfrydrychewicz/ogre/internal/index"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// Resolve decides what a request for escapedPath, the raw and still
// percent-encoded path, should get (PRD 5.5 and 5.6). A query string or
// fragment on it is ignored.
//
// It reads only the in-memory snapshot and never the database, so a redirect
// costs no I/O and keeps working while a write is in progress. One snapshot is
// loaded per call: the whole decision, suggestions included, is made against
// a single consistent set of links. It logs nothing; what may be logged about
// a decision is its Kind and LinkName (NFR-17).
func (a *App) Resolve(escapedPath string) Decision {
	segments, err := linkmodel.SplitPath(escapedPath)
	if err != nil {
		return Decision{Kind: DecisionBadRequest}
	}
	if len(segments) == 0 {
		return Decision{Kind: DecisionHome}
	}
	snapshot := a.index.Load()
	link, ok := snapshot.Find(segments[0])
	if !ok {
		return a.unknownName(snapshot, segments)
	}

	// Which of the link's routes answers, and where it leads, is the link
	// model's rule.
	m, ok, reasons, err := link.Match(segments)
	return matched(link, m, ok, reasons, err)
}

// matched turns what the link model said about a request for a known name
// into the decision.
func matched(link *linkmodel.CompiledLink, m linkmodel.Match, ok bool, reasons []linkmodel.Reason, err error) Decision {
	switch {
	case err != nil:
		// The destination did not expand safely. Nothing is redirected, and
		// the half-made answer is dropped.
		return Decision{Kind: DecisionInternalError, LinkName: link.Link.Name, Link: &link.Link, Err: err}
	case !ok:
		return Decision{Kind: DecisionMissKnown, LinkName: link.Link.Name, Link: &link.Link, Reasons: reasons}
	}
	return Decision{
		Kind:        DecisionHit,
		LinkName:    link.Link.Name,
		Destination: m.Destination,
		Link:        &link.Link,
		Route:       routeOf(link, m.Route),
		Values:      m.Values,
	}
}

// routeOf returns the address of the snapshot's own copy of the route that
// won, so that a hit does not allocate a route per request. The store numbers
// positions 0, 1, 2, ... in route order, so the position is normally the
// index; anything else is searched for, and as a last resort copied.
func routeOf(link *linkmodel.CompiledLink, won linkmodel.Route) *linkmodel.Route {
	routes := link.Link.Routes
	same := func(i int) bool { return routes[i].ID == won.ID && routes[i].Pattern == won.Pattern }
	if i := won.Position; i >= 0 && i < len(routes) && same(i) {
		return &routes[i]
	}
	for i := range routes {
		if same(i) {
			return &routes[i]
		}
	}
	// A copy made here, and not the parameter's address: taking that would
	// move the parameter to the heap on every call, hit or not.
	orphan := won
	return &orphan
}

// unknownName answers a request whose first segment is no link's name: a
// miss, with the links the user may have meant (PRD 5.6, MISS-1).
//
// For each name close enough to the requested one, the rest of the request
// is tried against that link. When it resolves, the suggestion is the whole
// corrected request, values carried over, and following it leads straight to
// the destination; when it does not, the suggestion is the bare name, and the
// link comes with it (Suggestion.Link) so that the page can show how it is
// used. The order (fewest edits, then by name)
// and the limit are the link model's (linkmodel.SuggestNames).
//
// Values are not carried over when one of them is "." or "..": a browser
// removes such segments from a link before requesting it, so the corrected
// request would not be the one that was checked here.
func (a *App) unknownName(snapshot *index.Snapshot, segments []string) Decision {
	name := requestedName(segments[0])
	d := Decision{Kind: DecisionMissUnknown, LinkName: name, RequestedName: name}
	if name == "" {
		// Not text that could be a name. Nothing of it is repeated, and
		// nothing is suggested for it.
		return d
	}
	near := linkmodel.SuggestNames(name, snapshot.Names(), a.suggest)
	if len(near) == 0 {
		return d
	}
	d.Suggestions = make([]Suggestion, 0, len(near))
	// The request with its first segment replaced. One copy serves every
	// candidate, and the caller's slice is left alone.
	corrected := append([]string(nil), segments...)
	carry := !slices.ContainsFunc(segments[1:], func(seg string) bool { return seg == "." || seg == ".." })
	for _, candidate := range near {
		link, ok := snapshot.Find(candidate.Name)
		if !ok {
			continue // not reachable: the names come from this snapshot
		}
		s := Suggestion{
			Name:        link.Link.Name,
			Path:        link.Link.Name,
			Edits:       candidate.Edits,
			Description: link.Link.Description,
			Link:        &link.Link,
		}
		corrected[0] = link.Link.Name
		// A destination that fails to expand is treated as not resolving: a
		// miss page must not fail over a link the user did not even ask for.
		if !carry {
			d.Suggestions = append(d.Suggestions, s)
			continue
		}
		if m, ok, _, err := link.Match(corrected); ok && err == nil {
			s.Resolves, s.Destination, s.Path = true, m.Destination, joinPath(corrected)
		}
		d.Suggestions = append(d.Suggestions, s)
	}
	return d
}

// joinPath is the inverse of linkmodel.SplitPath: it percent-encodes each
// segment and joins them with "/", so that a value containing a slash, a
// space or a "?" stays one segment when the path is requested again.
func joinPath(segments []string) string {
	var b strings.Builder
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(url.PathEscape(seg))
	}
	return b.String()
}

// requestedName is what an unknown-name miss may say was asked for: the first
// segment in lowercase when it could be a link name, and nothing otherwise.
//
// The segment is arbitrary decoded text. "%2F" puts further parts of the path
// into it, and it can carry line breaks, markup, or a megabyte of anything;
// repeating that in a log or a response would record what NFR-17 says must
// not be recorded and reflect what an attacker chose. Text that cannot be a
// name is of no use to "create this link" or to suggestions either.
func requestedName(segment string) string {
	if !linkmodel.PossibleName(segment) {
		return ""
	}
	return linkmodel.NormalizeName(segment)
}
