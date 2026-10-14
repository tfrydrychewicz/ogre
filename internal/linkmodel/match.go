package linkmodel

import (
	"fmt"
	"strings"
)

// Reason says why one route of a link did not answer a request. A miss on a
// known name carries one Reason per route, so the page can show, next to
// each route, what about the request did not fit (PRD 5.6, MISS-2).
type Reason struct {
	// RouteID and Pattern identify the route.
	RouteID int64
	Pattern string
	// Code is one of the Reason codes: "segment_count", "literal_mismatch",
	// "value_rejected", "value_not_allowed" or "route_invalid".
	Code string
	// Variable is the variable that refused its value; empty for the codes
	// that are not about a variable.
	Variable string
	// Value is the request's segment that did not fit: the refused value,
	// or the text found where a literal was expected. It is the request's
	// own text, of any length and content; quote it before showing it.
	Value string
	// Accepts says what would have fit: the variable's Describe text, or
	// the literal the route expects.
	Accepts string
	// Message is the whole reason as a plain sentence, ready to show. It
	// quotes the value and cuts it short when it is long.
	Message string
}

// Match is the answer to a request that resolved: the route that won, the
// values its variables captured, and where to redirect.
type Match struct {
	// Route is the stored route, as in Link.Routes.
	Route Route
	// Values maps each variable of the route to the request's segment at
	// its position, exactly as typed. It is nil for a route without
	// variables. Values are what the user typed into a URL: show them, never
	// log them (NFR-17).
	Values map[string]string
	// Destination is the route's destination with the values substituted.
	Destination string
}

// CompiledLink is a link ready for matching: every route's pattern and
// destination are parsed once, when the link enters the index, and not again
// for each request. That includes compiling every regular expression. It is
// immutable after Compile and safe for concurrent use.
type CompiledLink struct {
	// Link is the link as stored. Its Routes are shared with the compiled
	// form: read them, never modify them.
	Link   Link
	routes []compiledRoute // routes[i] belongs to Link.Routes[i]
}

// compiledRoute is one route ready for matching.
type compiledRoute struct {
	pattern  Pattern
	template Template
	// valid is false for a route whose stored text cannot be read. Such a
	// route never matches.
	valid bool
	// noDots[i] is true when segment i is a variable that must refuse "."
	// and "..", because its value goes into the destination's path.
	noDots []bool
}

// Compile prepares l for matching.
//
// A route whose saved pattern or destination can no longer be read is kept
// in Link.Routes but never matches. Everything is validated before it is
// saved, so this only concerns data written by something other than Ogre;
// refusing to start over one such route would take every other link down
// with it.
//
// The save rules are not applied again here. A destination that was saved
// before a rule existed keeps working (see storedTemplate).
func Compile(l Link) CompiledLink {
	c := CompiledLink{Link: l, routes: make([]compiledRoute, len(l.Routes))}
	for i, r := range l.Routes {
		pattern, ok := StoredPattern(r.Pattern)
		if !ok {
			continue
		}
		template, ok := storedTemplate(r.Destination, pattern)
		if !ok {
			continue
		}
		route := compiledRoute{pattern: pattern, template: template, valid: true}
		for j, seg := range pattern.segments {
			if seg.Type != nil && template.InPath(seg.Variable) {
				if route.noDots == nil {
					route.noDots = make([]bool, len(pattern.segments))
				}
				route.noDots[j] = true
			}
		}
		c.routes[i] = route
	}
	return c
}

// Match finds the route that answers the request segments (as returned by
// SplitPath) and expands its destination: PRD 5.5, steps 3 to 5.
//
// A route matches when it has as many segments as the request, every literal
// equals the request's segment at that position ignoring case, and every
// variable accepts the value at its position. Of several matching routes the
// most specific wins: they are compared segment by segment from the left,
// where a literal beats a restricted variable (int, number, regex) and that
// beats a text variable; the first difference decides. A tie goes to the
// route with the lower Position, the one the owner listed first.
//
// When no route matches, ok is false and reasons has one entry per route, in
// route order, saying why that route did not fit.
//
// err is non-nil only when the winning route's destination did not expand to
// a URL on its own scheme and host (ErrExpand). That is a bug, not a miss:
// answer 500 and do not redirect.
//
// The choice lives here, not in the resolver, so that the rules of PRD
// section 5 have a single implementation (NFR-22). A request for a route
// without variables allocates nothing.
func (c CompiledLink) Match(segments []string) (m Match, ok bool, reasons []Reason, err error) {
	// Where each route stopped fitting, kept from the one pass over the
	// routes so that explaining a miss does not check every value a second
	// time: a regular expression is the costly part of matching, and a miss
	// is what an attacker can ask for at will (NFR-8). The array lives on
	// the stack; only a link with more routes than can be saved needs more.
	var buf [MaxRoutes]mismatch
	found := buf[:0]
	if len(c.routes) > len(buf) {
		found = make([]mismatch, 0, len(c.routes))
	}

	best := -1
	for i := range c.routes {
		r := &c.routes[i]
		at := mismatch{at: routeUnreadable}
		if r.valid {
			at = r.mismatch(segments)
		}
		found = append(found, at)
		if at.at != noMismatch {
			continue
		}
		if best < 0 || c.moreSpecific(i, best) {
			best = i
		}
	}
	if best < 0 {
		return Match{}, false, c.reasons(segments, found), nil
	}

	r := &c.routes[best]
	m = Match{Route: c.Link.Routes[best]}
	if len(r.pattern.variables) > 0 {
		m.Values = make(map[string]string, len(r.pattern.variables))
		for i, seg := range r.pattern.segments {
			if seg.Type != nil {
				m.Values[seg.Variable] = segments[i]
			}
		}
	}
	if m.Destination, err = r.template.Expand(m.Values); err != nil {
		return Match{}, false, nil, fmt.Errorf("link %q, route %d: %w", c.Link.Name, m.Route.ID, err)
	}
	return m, true, nil, nil
}

// Accepts reports whether a route with pattern p and destination t would
// accept value for the named variable: the variable's type accepts it and,
// when the variable is used in the destination's path, no step of it is "."
// or ".." (NFR-10). declared is false when p has no such variable.
//
// It is the rule Match applies to one segment, for checking sample values
// against a route that is still being written. Pass the zero Template when
// the destination did not parse; only the type is then asked.
func Accepts(p Pattern, t Template, variable, value string) (accepted, declared bool) {
	seg, declared := p.variable(variable)
	if !declared {
		return false, false
	}
	return valueFits(seg.Type, t.InPath(variable), value) == "", true
}

// CheckValue is Accepts with the reason a refused value would get from a
// saved route: the same sentence a miss page shows for that variable, in
// the words of meta (the route's notes on its variables, which may be nil).
// reason is the zero Reason when the value is accepted or the variable is
// not declared. RouteID and Pattern of the reason are not set.
//
// It lets a form or a launcher judge one value as it is typed, before the
// other values of the go link are known (VAR-5).
func CheckValue(p Pattern, t Template, meta []VariableMeta, variable, value string) (reason Reason, accepted, declared bool) {
	seg, declared := p.variable(variable)
	if !declared {
		return Reason{}, false, false
	}
	code := valueFits(seg.Type, t.InPath(variable), value)
	if code == "" {
		return Reason{}, true, true
	}
	var note VariableMeta
	for _, m := range meta {
		if m.Name == variable {
			note = m
			break
		}
	}
	return valueReason(seg, note, value, code), false, true
}

// valueFits returns "" when a variable of type typ accepts value, and
// otherwise the reason code. dotsRefused says that the variable is used in
// the destination's path.
func valueFits(typ VarType, dotsRefused bool, value string) string {
	if !typ.Accepts(value) {
		return ReasonValueRejected
	}
	if dotsRefused && hasDotStep(value) {
		return ReasonValueNotAllowed
	}
	return ""
}

// hasDotStep reports whether value is "." or "..", or has one of the two
// between slashes: "../x", "a/../b", "x/..", and the same with "\".
//
// A value arrives in the destination percent-encoded, so "../x" is sent on
// as "..%2Fx", which is one path segment to a browser. Many servers decode a
// path before they normalise it, though, and to those the value is a step
// up and a name: the request would climb out of the path the owner wrote,
// which is what NFR-10 exists to prevent. The Directory cannot know which
// kind of server a destination is, so it refuses the value. A backslash
// counts as a slash because browsers and some servers treat it as one.
//
// A value that merely contains dots ("v1..2", "...", ".hidden") is fine.
func hasDotStep(value string) bool {
	// Nearly every value has no dot at all; decide that without a scan per
	// step.
	if strings.IndexByte(value, '.') < 0 {
		return false
	}
	for start := 0; start <= len(value); {
		end := start
		for end < len(value) && value[end] != '/' && value[end] != '\\' {
			end++
		}
		if step := value[start:end]; step == "." || step == ".." {
			return true
		}
		start = end + 1
	}
	return false
}

// mismatch says where a route stopped fitting a request.
type mismatch struct {
	// at is the index of the first segment that does not fit, or one of
	// noMismatch, wrongCount and routeUnreadable.
	at int
	// notAllowed says that the segment at that index is a value its type
	// accepts and the rule against "." and ".." refuses (NFR-10).
	notAllowed bool
}

// What mismatch.at holds when there is no segment to point at.
const (
	noMismatch      = -1 // the route matches
	wrongCount      = -2 // the request has more or fewer segments than the pattern
	routeUnreadable = -3 // the stored route cannot be read, so it never matches
)

// mismatch returns where the request stops fitting the route. Each value is
// checked once, here; see Match.
func (r *compiledRoute) mismatch(segments []string) mismatch {
	pattern := r.pattern.segments
	if len(pattern) != len(segments) {
		return mismatch{at: wrongCount}
	}
	for i, seg := range pattern {
		if seg.Type == nil {
			if !equalFoldASCII(seg.Literal, segments[i]) {
				return mismatch{at: i}
			}
		} else if code := valueFits(seg.Type, r.noDots != nil && r.noDots[i], segments[i]); code != "" {
			return mismatch{at: i, notAllowed: code == ReasonValueNotAllowed}
		}
	}
	return mismatch{at: noMismatch}
}

// rank orders the kinds of segment by how specific they are; lower is more
// specific (PRD 5.5, step 4).
func rank(seg Segment) int {
	switch {
	case seg.Type == nil:
		return 0 // a literal
	case seg.Type.Restricted():
		return 1
	}
	return 2 // a text variable
}

// moreSpecific reports whether route i wins over route j, both of which
// match the same request and so have the same number of segments.
func (c CompiledLink) moreSpecific(i, j int) bool {
	a, b := c.routes[i].pattern.segments, c.routes[j].pattern.segments
	for k := range a {
		if ra, rb := rank(a[k]), rank(b[k]); ra != rb {
			return ra < rb
		}
	}
	// Equally specific: the owner's order decides. Routes normally arrive
	// sorted by position; comparing the field keeps the rule true when they
	// do not.
	return c.Link.Routes[i].Position < c.Link.Routes[j].Position
}

// reasons explains, for every route, why it did not answer the request.
// found[i] is where route i stopped fitting, as Match found it.
func (c CompiledLink) reasons(segments []string, found []mismatch) []Reason {
	if len(c.routes) == 0 {
		return nil
	}
	reasons := make([]Reason, len(c.routes))
	for i := range c.routes {
		r := &c.routes[i]
		reason := Reason{RouteID: c.Link.Routes[i].ID, Pattern: c.Link.Routes[i].Pattern}
		switch at := found[i].at; {
		case at == routeUnreadable, at == noMismatch:
			// noMismatch does not occur: reasons are only asked for when
			// nothing matched.
			reason.Code = ReasonRouteInvalid
			reason.Message = "This route's saved pattern or destination can't be read, so it never matches. Edit the link to fix it."
		case at == wrongCount:
			reason.Code = ReasonSegmentCount
			reason.Message = countMessage(r.pattern, len(segments))
		case r.pattern.segments[at].Type == nil:
			seg := r.pattern.segments[at]
			reason.Code = ReasonLiteralMismatch
			reason.Value, reason.Accepts = segments[at], seg.Literal
			reason.Message = fmt.Sprintf("The request has %s where this route expects %q.",
				quoteForMessage(segments[at]), seg.Literal)
		default:
			seg := r.pattern.segments[at]
			code := ReasonValueRejected
			if found[i].notAllowed {
				code = ReasonValueNotAllowed
			}
			reason = valueReason(seg, variableMetaOf(c.Link.Routes[i], seg.Variable), segments[at], code)
			reason.RouteID, reason.Pattern = c.Link.Routes[i].ID, c.Link.Routes[i].Pattern
		}
		reasons[i] = reason
	}
	return reasons
}

// valueReason explains why the variable of seg refused value; code is
// ReasonValueRejected or ReasonValueNotAllowed (valueFits). The sentence
// names the variable in the owner's words when there are any, "month
// (Month, 1 to 12)", because the description exists to explain the variable
// to people (MISS-2), and ends with the owner's example. RouteID and Pattern
// are left for the caller.
func valueReason(seg Segment, meta VariableMeta, value, code string) Reason {
	reason := Reason{Code: code, Variable: seg.Variable, Value: value, Accepts: seg.Type.Describe()}
	who := seg.Variable
	if meta.Description != "" {
		who += " (" + meta.Description + ")"
	}
	switch {
	case code == ReasonValueNotAllowed && (value == "." || value == ".."):
		reason.Message = fmt.Sprintf(
			"%s can't be %q, because its value becomes part of the destination's path.",
			who, value)
	case code == ReasonValueNotAllowed:
		reason.Message = fmt.Sprintf(
			"%s can't be %s: a value that becomes part of the destination's path can't have \".\" or \"..\" between slashes.",
			who, quoteForMessage(value))
	default:
		reason.Message = fmt.Sprintf("%s doesn't accept %s. It accepts %s.",
			who, quoteForMessage(value), seg.Type.Describe())
	}
	if meta.Example != "" {
		reason.Message += " For example: " + quoteForMessage(meta.Example) + "."
	}
	return reason
}

// variableMetaOf returns what the route stores about the named variable; the
// zero value when it stores nothing.
func variableMetaOf(r Route, name string) VariableMeta {
	for _, meta := range r.Variables {
		if meta.Name == name {
			return meta
		}
	}
	return VariableMeta{}
}

// countMessage says that a request has the wrong number of segments for a
// pattern, counting what follows the link's name, which is how a user thinks
// of it: "go/reports/10" gives reports one value.
func countMessage(p Pattern, got int) string {
	want := len(p.segments) - 1
	got--
	noun := "part"
	if want > 0 && len(p.variables) == want {
		noun = "value" // everything after the name is a variable
	}
	var needs, has string
	switch {
	case want == 0:
		needs = fmt.Sprintf("takes nothing after %q", p.Name())
	default:
		needs = fmt.Sprintf("needs %s after %q", plural(want, noun), p.Name())
	}
	switch {
	case got <= 0:
		has = "none"
	default:
		has = fmt.Sprint(got)
	}
	return fmt.Sprintf("This route %s, and the request has %s.", needs, has)
}
