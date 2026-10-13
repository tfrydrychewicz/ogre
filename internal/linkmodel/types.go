// Package linkmodel is the pure core of Ogre: the shape of a link, the rules
// for what a valid link looks like, and how a request path is cut into
// segments. It performs no I/O and imports only the standard library, so the
// same rules can be exercised by the server, the API validation endpoint and
// fuzz tests without any setup.
package linkmodel

import "time"

// Severity values of a Problem. An error blocks saving; a warning is advice
// the user may ignore.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Field values of a Problem: which input of a route the problem is about.
const (
	FieldPattern     = "pattern"
	FieldDestination = "destination"
)

// Problem describes one thing wrong with a pattern or a destination. It
// carries byte offsets so an editor can underline the exact spot instead of
// only saying that the whole field is invalid.
type Problem struct {
	// Severity is "error" or "warning".
	Severity string
	// Field is "pattern" or "destination".
	Field string
	// Code is a stable identifier such as "pattern.empty". Clients and tests
	// key on it; the Message may be reworded freely.
	Code string
	// Message is a plain sentence a user can read.
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
// Pattern is the canonical text (Pattern.Canonical). The parsed form is not
// kept here: it is derived from the text when the link is compiled for
// matching (Compile), so a Route stays a plain value that the store can save
// and compare.
type Route struct {
	ID          int64
	Position    int
	Pattern     string
	Destination string
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
