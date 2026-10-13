// Package linkmodel is the pure core of Ogre: everything section 5 of the
// PRD says about what a link is and how a request is resolved, and nothing
// else (NFR-22).
//
//   - A pattern such as "gh/{repo}/pr/{num:int}" is parsed by ParsePattern
//     into literal and variable segments; a variable has a type (VarType)
//     that says which values it accepts.
//   - A destination such as "https://github.com/acme/{repo}/pull/{num}" is
//     parsed by ParseDestination into a Template, which Expand turns into
//     the URL to redirect to.
//   - ValidateDraft applies every rule for saving a link that needs no
//     stored data; saving and "check without saving" both call it.
//   - SplitPath cuts a request path into segments, Compile prepares a link,
//     and CompiledLink.Match picks the route that answers and says, for a
//     miss, why each route did not.
//   - Distance and SuggestNames find the names a mistyped one may have
//     meant.
//
// The package performs no I/O and imports only the standard library, so the
// same rules can be exercised by the server, the API validation endpoint and
// fuzz tests without any setup. Every code it reports is listed in codes.go.
package linkmodel

import "time"

// Severity values of a Problem. An error blocks saving; a warning is advice
// the user may ignore.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Field values of a Problem: what the problem is about. A form places a
// problem by its field: beside the pattern or the destination, with the range
// Start..End underlined, or, for the other two, beside the route or the link
// as a whole, where there is no text to underline and both offsets are 0.
const (
	FieldPattern     = "pattern"
	FieldDestination = "destination"
	// FieldRoute: the route as a whole, such as one that names a saved route
	// the link does not have.
	FieldRoute = "route"
	// FieldLink: the link as a whole, such as a draft without routes.
	FieldLink = "link"
)

// Problem describes one thing wrong with a pattern or a destination, or one
// thing worth a warning. It carries byte offsets so an editor can underline
// the exact spot instead of only saying that the whole field is invalid.
type Problem struct {
	// Severity is "error" or "warning".
	Severity string
	// Field is "pattern", "destination", "route" or "link" (see FieldLink).
	Field string
	// Code is a stable identifier such as "pattern.empty". Clients and tests
	// key on it; the Message may be reworded freely. The codes are listed in
	// codes.go.
	Code string
	// Message is a plain sentence that says what to do, for someone who is
	// not a programmer.
	Message string
	// Start and End are byte offsets into the input ([Start, End)) marking
	// where the problem is. Bytes are what Go strings index by; the HTTP
	// layer converts them to UTF-16 code units for the browser, which counts
	// that way.
	Start, End int
}

// Route is one pattern-to-destination mapping of a link. A link has one or
// more routes; Position is their order as the owner arranged them.
//
// Pattern is the canonical text (Pattern.String) and Destination the
// template as the owner wrote it. Their parsed forms are not kept here: they
// are derived from the text when the link is compiled for matching
// (Compile), so a Route stays a plain value that the store can save.
type Route struct {
	ID          int64
	Position    int
	Pattern     string
	Destination string
	// Variables has one entry per variable of the pattern, in pattern order.
	// It carries what the pattern text cannot: a description and an example
	// for each variable. Both may be empty. A literal-only route has none.
	Variables []VariableMeta
}

// VariableMeta is what a route knows about one of its variables besides the
// pattern text: the words shown next to it in usage help and in the launcher.
// The name and the type live in the pattern, which stays the single source of
// truth for matching; this is stored beside it, per route.
type VariableMeta struct {
	// Name is the variable's name as written in the pattern.
	Name string
	// Description says what the value means, for people ("Month, 1 to 12").
	Description string
	// Example is a value the variable accepts, shown as a hint.
	Example string
}

// Link is a named go link together with its routes.
type Link struct {
	ID int64
	// Name is the first segment shared by all routes. It is always lowercase
	// (see NormalizeName).
	Name        string
	Description string
	OwnerID     int64
	// Version counts saved revisions, starting at 1; it is the basis for
	// optimistic concurrency on edits.
	Version   int
	Routes    []Route
	CreatedAt time.Time
	UpdatedAt time.Time
}
