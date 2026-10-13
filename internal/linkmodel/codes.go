package linkmodel

// This file is the one list of every problem and reason code. All but two
// are reported by the link model itself (see CodeRouteNotInLink). Codes are
// part of the API: clients and tests key on them, so a code never changes its
// meaning once released. The messages that go with them may be reworded
// freely.

// Problem codes for a pattern (Field "pattern"), reported by ParsePattern.
const (
	// CodePatternEmpty: the pattern is the empty string.
	CodePatternEmpty = "pattern.empty"
	// CodePatternTooLong: more than MaxPatternLength characters.
	CodePatternTooLong = "pattern.too_long"
	// CodePatternTooManySegments: more than MaxPatternSegments segments.
	CodePatternTooManySegments = "pattern.too_many_segments"
	// CodePatternTooManyVariables: more than MaxPatternVariables variables.
	CodePatternTooManyVariables = "pattern.too_many_variables"
	// CodePatternEmptySegment: a leading, trailing or doubled slash.
	CodePatternEmptySegment = "pattern.empty_segment"
	// CodePatternBadStart: a literal that does not begin with a letter or
	// digit.
	CodePatternBadStart = "pattern.bad_start"
	// CodePatternBadCharacter: a character a literal cannot contain, or a
	// raw control character (a line break, a tab) inside regex(…).
	CodePatternBadCharacter = "pattern.bad_character"
	// CodePatternReservedName: the first segment is a name browsers request
	// on their own (ReservedName).
	CodePatternReservedName = "pattern.reserved_name"
	// CodePatternVariableFirst: the first segment is a variable; it must be
	// the link's name.
	CodePatternVariableFirst = "pattern.variable_first"
	// CodePatternMixedSegment: fixed text and a variable, or two variables,
	// in one segment ("v{n}").
	CodePatternMixedSegment = "pattern.mixed_segment"
	// CodePatternUnclosedVariable: a "{" whose "}" is missing.
	CodePatternUnclosedVariable = "pattern.unclosed_variable"
	// CodePatternUnmatchedBrace: a "}" with no "{" before it.
	CodePatternUnmatchedBrace = "pattern.unmatched_brace"
	// CodePatternEmptyVariableName: "{}" or "{:int}".
	CodePatternEmptyVariableName = "pattern.empty_variable_name"
	// CodePatternBadVariableName: a variable name that is not letters,
	// digits and "_" beginning with a letter.
	CodePatternBadVariableName = "pattern.bad_variable_name"
	// CodePatternDuplicateVariable: a variable name used twice in one
	// pattern, ignoring case.
	CodePatternDuplicateVariable = "pattern.duplicate_variable"
	// CodePatternUnknownType: the text after the colon is not a type this
	// version knows. The message lists the types that exist.
	CodePatternUnknownType = "pattern.unknown_type"
	// CodePatternRegexUnbalanced: the parenthesis that closes "regex(" is
	// missing.
	CodePatternRegexUnbalanced = "pattern.regex_unbalanced"
	// CodePatternRegexTrailingText: something other than "}" follows the
	// parenthesis that closes "regex(".
	CodePatternRegexTrailingText = "pattern.regex_trailing_text"
	// CodePatternRegexEmpty: "regex()".
	CodePatternRegexEmpty = "pattern.regex_empty"
	// CodePatternRegexTooLong: an expression of more than MaxRegexLength
	// characters.
	CodePatternRegexTooLong = "pattern.regex_too_long"
	// CodePatternRegexInvalid: the expression is not valid RE2 syntax. The
	// message says what is wrong in plain words.
	CodePatternRegexInvalid = "pattern.regex_invalid"
	// CodePatternRegexTooComplex: the expression compiles to a program
	// larger than MaxRegexProgram, mostly through large repeat counts. Also
	// link-level (Field "link"), from ValidateDraft: the expressions of all
	// routes together are larger than MaxLinkRegexProgram.
	CodePatternRegexTooComplex = "pattern.regex_too_complex"
)

// Problem codes for the rules that concern a route among its link's other
// routes, or the link as a whole, reported by ValidateDraft. They carry the
// "pattern." prefix because a link's name and shape come from its patterns.
const (
	// CodeNoRoutes: a draft without routes. Link-level.
	CodeNoRoutes = "pattern.no_routes"
	// CodeTooManyRoutes: more than MaxRoutes routes. Link-level.
	CodeTooManyRoutes = "pattern.too_many_routes"
	// CodeDuplicateRouteID: two routes of a draft claim the same stored
	// route. Link-level.
	CodeDuplicateRouteID = "pattern.duplicate_route_id"
	// CodeNameMismatch: the pattern's first segment differs from the first
	// valid route's.
	CodeNameMismatch = "pattern.name_mismatch"
	// CodeDuplicatePattern: the pattern is, once canonical, the very text of
	// an earlier route's pattern.
	CodeDuplicatePattern = "pattern.duplicate"
	// CodeSameShape: the pattern differs from an earlier route's only in the
	// names of its variables (PRD 5.5).
	CodeSameShape = "pattern.same_shape"
	// CodeUnknownVariableMeta: a description or example was sent for a
	// variable the pattern does not declare.
	CodeUnknownVariableMeta = "pattern.unknown_variable_meta"
	// CodeDuplicateVariableMeta: two descriptions were sent for one
	// variable.
	CodeDuplicateVariableMeta = "pattern.duplicate_variable_meta"
	// CodeExampleRejected (warning): a variable's example is a value the
	// variable itself would not accept.
	CodeExampleRejected = "pattern.example_rejected"
)

// Problem codes for what only stored data can decide. The link model never
// reports them: the application does (package app), when a draft is checked
// against the link it edits. They are listed here so that this file stays
// the one list of every problem code a client can meet.
const (
	// CodeRouteNotInLink: a route of the draft carries the ID of a saved
	// route that the link being edited does not have.
	CodeRouteNotInLink = "pattern.route_not_in_link"
	// CodeLinkNotFound: a draft was checked against a link that does not
	// exist (any more). Link-level.
	CodeLinkNotFound = "link.not_found"
)

// Problem codes for a destination (Field "destination"), reported by
// ParseDestination.
const (
	// CodeDestinationEmpty: the destination is the empty string.
	CodeDestinationEmpty = "destination.empty"
	// CodeDestinationTooLong: more than MaxDestinationLength characters.
	CodeDestinationTooLong = "destination.too_long"
	// CodeDestinationBadCharacter: whitespace, a control character, an
	// invisible formatting character or invalid UTF-8.
	CodeDestinationBadCharacter = "destination.bad_character"
	// CodeDestinationNotAbsolute: no scheme at all.
	CodeDestinationNotAbsolute = "destination.not_absolute"
	// CodeDestinationScheme: a scheme other than http or https (NFR-9).
	CodeDestinationScheme = "destination.scheme"
	// CodeDestinationNoHost: http or https, but no host.
	CodeDestinationNoHost = "destination.no_host"
	// CodeDestinationInvalid: not parseable as a URL, a port that is not a
	// real port, or a placeholder that would finish a percent escape
	// ("%{x}").
	CodeDestinationInvalid = "destination.invalid"
	// CodeDestinationCredentials: a user name or password before the host.
	CodeDestinationCredentials = "destination.credentials"
	// CodeDestinationGoLink: the host is "go", so the destination is itself
	// a go link.
	CodeDestinationGoLink = "destination.go_link"
	// CodeDestinationDirectory: the destination is a URL on the Directory
	// itself (ValidateOptions.OwnHosts).
	CodeDestinationDirectory = "destination.directory"
	// CodeDestinationUnclosedPlaceholder: a "{" whose "}" is missing.
	CodeDestinationUnclosedPlaceholder = "destination.unclosed_placeholder"
	// CodeDestinationUnmatchedBrace: a "}" with no "{" before it.
	CodeDestinationUnmatchedBrace = "destination.unmatched_brace"
	// CodeDestinationBadPlaceholder: braces around something that is not a
	// variable name, such as "{}", "{{x}" or "{a:int}".
	CodeDestinationBadPlaceholder = "destination.bad_placeholder"
	// CodeDestinationPlaceholderInHost: a placeholder in the scheme, the
	// host, the port or anywhere else before the path.
	CodeDestinationPlaceholderInHost = "destination.placeholder_in_host"
	// CodeDestinationUndeclaredVariable: a placeholder for a variable the
	// pattern does not declare.
	CodeDestinationUndeclaredVariable = "destination.undeclared_variable"
	// CodeDestinationUnusedVariable (warning): a variable of the pattern
	// that the destination does not use.
	CodeDestinationUnusedVariable = "destination.unused_variable"
)

// Reason codes: why one route did not answer a request (Reason.Code).
const (
	// ReasonSegmentCount: the request has more or fewer segments than the
	// route's pattern.
	ReasonSegmentCount = "segment_count"
	// ReasonLiteralMismatch: a segment of the request differs from the
	// pattern's fixed text at that position.
	ReasonLiteralMismatch = "literal_mismatch"
	// ReasonValueRejected: a variable's type does not accept the value.
	ReasonValueRejected = "value_rejected"
	// ReasonValueNotAllowed: the value is "." or "..", or has one of them
	// between slashes ("../x"), and the variable is used in the destination's
	// path (NFR-10).
	ReasonValueNotAllowed = "value_not_allowed"
	// ReasonRouteInvalid: the route's stored pattern or destination cannot
	// be read, so the route never matches. Ogre validates before it saves;
	// this only concerns data written by something else.
	ReasonRouteInvalid = "route_invalid"
)
