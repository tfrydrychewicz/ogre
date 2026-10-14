package app

import (
	"errors"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// This file holds the types and errors that the HTTP layer compiles against:
// what a use case takes, what it returns and how it fails. The use cases
// themselves are in resolve.go, suggest.go, create.go, update.go and
// validate.go.

// Errors of the use cases that a caller is expected to handle. Test for them
// with errors.Is.
var (
	// ErrNameTaken means another link already has the draft's name. CreateLink
	// and UpdateLink return it as a *NameTakenError, which says which link.
	ErrNameTaken = errors.New("a link with this name already exists")
	// ErrForbidden means the actor is not allowed to do what was asked.
	ErrForbidden = errors.New("not allowed")
	// ErrNotFound means no link has the given ID.
	ErrNotFound = errors.New("no such link")
	// ErrVersionConflict means the link was changed since the caller read it:
	// the version sent with an update or a delete is not the stored one.
	// Nothing was written. UpdateLink and DeleteLink return it as a
	// *VersionConflictError, which says what the stored version is.
	ErrVersionConflict = errors.New("the link was changed by someone else")
)

// NameTakenError is how CreateLink and UpdateLink report that the name is in
// use. It carries what the caller needs to offer the way out (DIR-4): the
// link that holds the name, and whether the actor could edit that link
// instead of creating a second one. errors.Is(err, ErrNameTaken) is true.
type NameTakenError struct {
	// Existing is the link that holds the name.
	Existing linkmodel.Link
	// Editable says whether the actor who was refused may edit Existing.
	Editable bool
}

func (e *NameTakenError) Error() string {
	return "a link named " + e.Existing.Name + " already exists"
}

// Is makes errors.Is(err, ErrNameTaken) hold.
func (e *NameTakenError) Is(target error) bool { return target == ErrNameTaken }

// ForbiddenError is how UpdateLink and DeleteLink report that the actor may
// not change a link that exists. It carries what the refusal should say
// (PRD 5.7, save example 5: "the message names the owner and links to" the
// link): which link it is and who can change it. errors.Is(err, ErrForbidden)
// is true.
//
// A refusal that is not about one link, such as nobody acting at all, is the
// bare ErrForbidden.
type ForbiddenError struct {
	// Link is the link the actor may not change.
	Link linkmodel.Link
	// Owner is the display name of the link's owner; "" when the owner
	// cannot be named.
	Owner string
}

func (e *ForbiddenError) Error() string {
	return "not allowed to change the link named " + e.Link.Name
}

// Is makes errors.Is(err, ErrForbidden) hold.
func (e *ForbiddenError) Is(target error) bool { return target == ErrForbidden }

// VersionConflictError is how UpdateLink and DeleteLink report a stale
// version. errors.Is(err, ErrVersionConflict) is true.
type VersionConflictError struct {
	// CurrentVersion is the version the link is at now.
	CurrentVersion int
}

func (e *VersionConflictError) Error() string { return ErrVersionConflict.Error() }

// Is makes errors.Is(err, ErrVersionConflict) hold.
func (e *VersionConflictError) Is(target error) bool { return target == ErrVersionConflict }

// Problem codes that only this package can report, because they need stored
// data. They appear in a ValidationError and in a ValidateResult next to the
// link model's codes, and are declared with them (linkmodel/codes.go), so
// that there is one list of every code.
const (
	// CodeRouteNotInLink: a route of the draft carries the ID of a saved route
	// that the link being edited does not have.
	CodeRouteNotInLink = linkmodel.CodeRouteNotInLink
	// CodeLinkNotFound: Validate was asked about a link that does not exist.
	CodeLinkNotFound = linkmodel.CodeLinkNotFound
)

// Options configures an App. The zero value is a working configuration.
type Options struct {
	// Suggest tunes "Did you mean" for an unknown name: the allowed number of
	// edits by name length and the number of suggestions. The zero value means
	// the PRD's defaults (PRD 5.6).
	Suggest linkmodel.SuggestOptions
	// OwnHosts are the hosts the Directory itself answers on, in the forms
	// linkmodel.ValidateOptions.OwnHosts takes ("localhost:4655",
	// "https://go.example.com"). A destination on one of them is refused on
	// save, because a go link that leads back into the Directory can loop
	// (PRD 5.4). Empty means the rule is not applied.
	OwnHosts []string
}

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
	// DecisionInternalError: a route matched, but its destination did not
	// expand to a URL on the template's own scheme and host. That is a bug or
	// damaged data, never the request's fault: answer 500 and do not
	// redirect. Decision.Destination is empty.
	DecisionInternalError
)

// String returns a short name for logs, the API and test failures.
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
	case DecisionInternalError:
		return "internal_error"
	}
	return "unknown"
}

// Decision is the resolver's answer for one request.
//
// Link, Route, Values, Reasons and Suggestions point into, or are derived
// from, the snapshot the decision was made against: read them, never modify
// them.
type Decision struct {
	Kind DecisionKind
	// LinkName is the matched link's name for a hit, a known-name miss and an
	// internal error, and the requested name, lowercased, for an unknown-name
	// miss. It is only ever the first segment of the request and never the
	// raw path, so it is what request logs may record (NFR-17).
	//
	// It is always text that could be a link name (linkmodel.PossibleName).
	// It is empty for DecisionHome and DecisionBadRequest, and for an
	// unknown-name miss whose first segment could not be a name at all.
	LinkName string
	// Destination is where to redirect; set only for DecisionHit.
	Destination string

	// Link is the link that has the requested name: set for a hit, a
	// known-name miss and an internal error, nil otherwise.
	Link *linkmodel.Link
	// Route is the route that answered; set only for a hit.
	Route *linkmodel.Route
	// Values maps each variable of Route to what the request carried for it,
	// decoded and otherwise as typed; set only for a hit, and nil for a route
	// without variables. These are what a user typed into a URL: show them,
	// never log them (NFR-17).
	Values map[string]string
	// Reasons says, for a known-name miss, why each route did not fit: one
	// entry per route of Link, in route order (PRD 5.6, MISS-2).
	Reasons []linkmodel.Reason
	// Suggestions are the "Did you mean" entries of an unknown-name miss,
	// best first (PRD 5.6, MISS-1). Nil when there are none.
	Suggestions []Suggestion
	// RequestedName is, for an unknown-name miss, the name that was asked
	// for, lowercased; the same text as LinkName. Empty when the first
	// segment could not be a link name, and for every other kind.
	RequestedName string
	// Err says what went wrong for DecisionInternalError, for the server's
	// log; nil otherwise. Its text names the link and the route but carries
	// no value and nothing else of the request, so it may be logged
	// (NFR-17). It is not for the response.
	Err error
}

// Suggestion is one "Did you mean" entry: an existing link whose name is
// close to the one requested.
type Suggestion struct {
	// Name is the suggested link's name.
	Name string
	// Path is the corrected request: the suggested name followed by the rest
	// of what the user typed, when that resolves ("reports/10/13" for a
	// request of "reprots/10/13"), and otherwise Name alone. It has no
	// leading slash and is percent-encoded like a request path, so "/"+Path
	// can be handed to Resolve or used as the path of a link.
	Path string
	// Edits is how many edits separate the requested name from Name.
	Edits int
	// Resolves says whether Path resolves, which is the case exactly when
	// the user's values could be carried over. When it is false, following
	// the suggestion should show the link's usage help.
	Resolves bool
	// Destination is where Path leads; set only when Resolves.
	Destination string
	// Description is the suggested link's description.
	Description string
	// Link is the suggested link itself, in the snapshot: read it, never
	// modify it. It is what a page shows as usage help when Resolves is
	// false, since following Path would then not explain anything: a link
	// with a route for its bare name would simply redirect (PRD 5.6).
	Link *linkmodel.Link
}

// ValidateRequest asks whether a draft could be saved, without saving it.
type ValidateRequest struct {
	// LinkID is the link being edited, or 0 for a new link. A link's own name
	// is available to itself, and its routes' IDs may be kept.
	LinkID int64
	Draft  Draft
	// Samples optionally holds, per route of the draft (same order), values
	// to try on that route's variables: variable name -> values. It may be
	// shorter than the route list.
	Samples []map[string][]string
}

// RouteCheck is what Validate found out about one route of the draft.
type RouteCheck struct {
	// Tokens are the segments of the pattern that could be read, so that a
	// client can highlight the pattern without parsing it itself.
	Tokens []linkmodel.Token
	// Problems are the route's errors and warnings: the pattern's first, then
	// the destination's.
	Problems []linkmodel.Problem
	// Samples says, for each sample value sent, whether the route would
	// accept it for that variable: variable name -> value -> accepted. A
	// variable the pattern does not declare, or whose declaration cannot be
	// read yet, is left out; nil when no samples were sent.
	Samples map[string]map[string]bool
	// SampleReasons says why each refused sample value is refused: variable
	// name -> value -> the sentence a miss page would show for it
	// (linkmodel.CheckValue), in the words of the route's notes on its
	// variables. Accepted values have no entry; nil when no samples were
	// sent.
	SampleReasons map[string]map[string]string
}

// NameCheck is what Validate found out about the draft's name.
type NameCheck struct {
	// Value is the name the link would be saved under: the first segment,
	// lowercased, of the first pattern of the draft whose first segment can
	// be a name, whether or not the rest of that pattern is valid yet. Empty
	// when no pattern of the draft begins with a name.
	Value string
	// Available says that no other link has the name. It is false when Value
	// is empty.
	Available bool
	// Existing is the link that holds the name, when it is taken by another
	// link than the one being edited.
	Existing *linkmodel.Link
	// ExistingEditable says whether the actor may edit Existing.
	ExistingEditable bool
}

// ValidateResult is the answer of Validate.
type ValidateResult struct {
	// Valid says the draft would be saved as it is: no error anywhere (a
	// warning does not count) and the name is available.
	Valid bool
	Name  NameCheck
	// Routes has one entry per route of the draft, in the draft's order. It
	// is empty for a draft with more routes than a link can have
	// (linkmodel.MaxRoutes): such a draft is refused as a whole and its
	// routes are not read.
	Routes []RouteCheck
	// Problems are the ones that belong to no single route.
	Problems []linkmodel.Problem
}
