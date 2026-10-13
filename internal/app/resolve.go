package app

import "github.com/tfrydrychewicz/ogre/internal/linkmodel"

// DecisionKind says what the resolver decided to do with a request.
type DecisionKind int

// The possible decisions. The HTTP layer maps each to a response; it never
// re-derives one from the path.
const (
	// DecisionHome: the path is empty, so there is no go link in it.
	DecisionHome DecisionKind = iota
	// DecisionHit: a route matched; redirect to Decision.Destination.
	DecisionHit
	// DecisionMissUnknown: no link has the requested name.
	DecisionMissUnknown
	// DecisionMissKnown: the link exists but none of its routes fits.
	DecisionMissKnown
	// DecisionBadRequest: the path cannot be decoded.
	DecisionBadRequest
)

// String returns a short name for logs and test failures.
func (k DecisionKind) String() string {
	switch k {
	case DecisionHome:
		return "home"
	case DecisionHit:
		return "hit"
	case DecisionMissUnknown:
		return "miss_unknown"
	case DecisionMissKnown:
		return "miss_known"
	case DecisionBadRequest:
		return "bad_request"
	}
	return "unknown"
}

// Decision is the resolver's answer for one request.
type Decision struct {
	Kind DecisionKind
	// LinkName is the matched link's name for a hit or a known-name miss,
	// and the requested name, lowercased, for an unknown-name miss. It is
	// only ever the first segment of the request and never the raw path, so
	// it is what request logs may record (NFR-17).
	//
	// It is always text that could be a link name (linkmodel.PossibleName).
	// It is empty for DecisionHome and DecisionBadRequest, and for an
	// unknown-name miss whose first segment could not be a name at all.
	LinkName string
	// Destination is where to redirect; set only for DecisionHit.
	Destination string
}

// Resolve decides what a request for escapedPath, the raw and still
// percent-encoded path, should get (PRD 5.5).
//
// It reads only the in-memory snapshot and never the database, so a redirect
// costs no I/O and keeps working while a write is in progress. One snapshot is
// loaded per call: the whole decision is made against a single consistent set
// of links.
func (a *App) Resolve(escapedPath string) Decision {
	segments, err := linkmodel.SplitPath(escapedPath)
	if err != nil {
		return Decision{Kind: DecisionBadRequest}
	}
	if len(segments) == 0 {
		return Decision{Kind: DecisionHome}
	}

	link, ok := a.index.Load().Find(segments[0])
	if !ok {
		return Decision{Kind: DecisionMissUnknown, LinkName: requestedName(segments[0])}
	}
	// Which of the link's routes answers is the link model's rule.
	if route, ok := link.Match(segments); ok {
		return Decision{Kind: DecisionHit, LinkName: link.Link.Name, Destination: route.Destination}
	}
	return Decision{Kind: DecisionMissKnown, LinkName: link.Link.Name}
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
