package app

import (
	"context"
	"slices"
	"strings"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// Validate says whether a draft could be saved, and everything a form needs
// to show while it is being written: the problems, the name and whether it is
// free, the tokens of each pattern, and whether sample values would be
// accepted.
//
// It is a pure check. Nothing is written, and the store is not read: the
// rules are the same function that saving calls (linkmodel.ValidateDraft,
// NFR-22), and what is already taken is answered by the index. The answer is
// therefore as of this moment; saving decides again.
//
// Two things are not checked: that the actor may edit the link named by
// LinkID, and that a version is current. Both are properties of a save, not
// of the draft. The actor is used only to say whether a link that already
// holds the name is one they could edit instead (DIR-4).
func (a *App) Validate(_ context.Context, actor Actor, req ValidateRequest) ValidateResult {
	snapshot := a.index.Load()
	link, problems := linkmodel.ValidateDraft(req.Draft, a.rules)

	res := ValidateResult{Problems: problems.Link}
	if len(req.Draft.Routes) > linkmodel.MaxRoutes {
		// Refused as a whole, and not read route by route: a request has
		// room for thousands of routes, and everything below costs time per
		// route. There is then no name and no per-route answer.
		return res
	}
	res.Routes = make([]RouteCheck, len(req.Draft.Routes))
	for i := range res.Routes {
		if i < len(problems.Tokens) {
			res.Routes[i].Tokens = problems.Tokens[i]
		}
		if i < len(problems.Routes) {
			res.Routes[i].Problems = problems.Routes[i]
		}
	}

	// What needs stored data: the link being edited and its routes.
	if req.LinkID != 0 {
		editing, ok := snapshot.ByID(req.LinkID)
		if !ok {
			res.Problems = append(res.Problems, linkmodel.Problem{
				Severity: linkmodel.SeverityError, Field: linkmodel.FieldLink, Code: CodeLinkNotFound,
				Message: "This link no longer exists. It may have been deleted.",
			})
		} else {
			for i, p := range foreignRoutes(req.Draft, editing.Link) {
				res.Routes[i].Problems = append(res.Routes[i].Problems, p...)
			}
		}
	}

	// The name. A valid draft has it; for an invalid one it is still worth
	// telling the user about the name they are typing.
	// Also when a pattern is not finished: "reports/{month:int}/{day" already
	// says the link is going to be called reports (plan section 3.5).
	res.Name.Value = link.Name
	for i := 0; res.Name.Value == "" && i < len(req.Draft.Routes); i++ {
		res.Name.Value, _ = linkmodel.PatternSoFar(req.Draft.Routes[i].Pattern)
	}
	if res.Name.Value != "" {
		holder, taken := snapshot.Find(res.Name.Value)
		switch {
		case !taken, req.LinkID != 0 && holder.Link.ID == req.LinkID:
			res.Name.Available = true
		default:
			existing := cloneLink(holder.Link)
			res.Name.Existing = &existing
			res.Name.ExistingEditable = canEdit(actor, existing)
		}
	}

	for i := range res.Routes {
		if i < len(req.Samples) && len(req.Samples[i]) > 0 {
			res.Routes[i].Samples, res.Routes[i].SampleReasons = trySamples(req.Draft.Routes[i], req.Samples[i], a.rules)
		}
	}

	res.Valid = res.Name.Available && !linkmodel.HasErrors(res.Problems)
	for _, r := range res.Routes {
		res.Valid = res.Valid && !linkmodel.HasErrors(r.Problems)
	}
	return res
}

// trySamples says, for each sample value, whether the route would accept it
// for that variable, and why not when it would not. The answer is the link
// model's (linkmodel.CheckValue): the variable's type, and the rule that a
// variable used in the destination's path refuses "." and ".." (NFR-10). It
// is what Match would decide for that segment once the route is saved, in
// the sentence a miss page would show.
//
// A pattern that is not valid yet is asked about the variables that can
// already be read (linkmodel.PatternSoFar), so a form can show what a value
// would do while the rest of the pattern is still being typed. A destination
// that cannot be read against those variables leaves only the types to
// decide. Samples for a variable that cannot be read, or that the pattern
// does not declare, are left out of the result.
func trySamples(r DraftRoute, samples map[string][]string, rules linkmodel.ValidateOptions) (map[string]map[string]bool, map[string]map[string]string) {
	_, pattern := linkmodel.PatternSoFar(r.Pattern)
	template, problems := linkmodel.ParseDestination(r.Destination, pattern, rules)
	if linkmodel.HasErrors(problems) {
		template = linkmodel.Template{}
	}
	result := make(map[string]map[string]bool, len(samples))
	reasons := make(map[string]map[string]string)
	for variable, values := range samples {
		if !slices.Contains(pattern.Variables(), variable) {
			continue
		}
		accepted := make(map[string]bool, len(values))
		for _, value := range values {
			reason, ok, _ := linkmodel.CheckValue(pattern, template, r.Variables, variable, value)
			accepted[value] = ok
			if !ok {
				if reasons[variable] == nil {
					reasons[variable] = make(map[string]string)
				}
				reasons[variable][value] = reason.Message
			}
		}
		result[variable] = accepted
	}
	return result, reasons
}

// ExpressionProblem checks a regular expression on its own, as the form's
// variables table holds it in a field of its own (VAR-3): the code and
// sentence of its first problem, in the terms of the expression alone, or
// two empty strings when the expression can be a regex variable's
// (linkmodel.RegexProblem). Inside the pattern, an unclosed "[" or "(" could
// only be reported as a missing parenthesis of regex(, which is syntax the
// table's author never wrote.
func ExpressionProblem(expression string) (code, message string) {
	return linkmodel.RegexProblem(expression)
}

// The two answers below serve the create and edit form (plan L3.6) beside
// Validate: what the table of a route's variables shows, and the live
// preview of a draft that is not saved yet. They are functions of the draft
// alone and read nothing stored, so the HTTP layer calls them directly after
// Validate; all they know about links they ask of the link model (NFR-22).

// VariableCheck is one variable of a pattern being written, as the form's
// variables table shows it.
type VariableCheck struct {
	Name string
	// Type is the type's name: "string", "int", "number" or "regex".
	Type string
	// Accepts says in words what the variable accepts (VarType.Describe).
	Accepts string
	// Expression is the regular expression of a regex variable, as written
	// between "regex(" and its closing parenthesis; "" for the other types.
	// The form shows it in its own field, so nobody has to find it inside
	// the pattern.
	Expression string
	// Start and End are the byte offsets of the variable's text in the
	// pattern, braces included ([Start, End)): the span a form replaces when
	// the variable's type is changed in the table.
	Start, End int
}

// DraftVariables lists the variables of a pattern that can be read so far,
// in pattern order, whether or not the pattern as a whole is valid yet. A
// variable whose name is used twice is listed once, at its first place.
func DraftVariables(pattern string) []VariableCheck {
	_, readable := linkmodel.PatternSoFar(pattern)
	out := make([]VariableCheck, 0, len(readable.Variables()))
	for _, seg := range readable.Segments() {
		if !seg.IsVariable() {
			continue
		}
		v := VariableCheck{
			Name: seg.Variable, Type: seg.Type.Name(), Accepts: seg.Type.Describe(),
			Start: seg.Start, End: seg.End,
		}
		if v.Type == linkmodel.TypeRegex {
			// Format is the canonical "regex(<expression>)"; the expression
			// is what it wraps, exactly as written.
			v.Expression = strings.TrimSuffix(strings.TrimPrefix(seg.Type.Format(), linkmodel.TypeRegex+"("), ")")
		}
		out = append(out, v)
	}
	return out
}

// PreviewRequest asks where a draft would send a request once it is saved.
type PreviewRequest struct {
	// Route is the index of the draft's route the request was built for, or
	// -1 for none in particular. When it is set, a route that cannot be
	// saved yet is reported instead of matched around, and a miss explains
	// that route only.
	Route int
	// Path is the go-link path, percent-encoded as it would follow "go/".
	Path string
}

// The kinds of a Preview. Three of them are the resolver's own decisions
// (DecisionKind.String); the last exists only for a draft.
const (
	PreviewHit        = "hit"
	PreviewMissKnown  = "miss_known"
	PreviewBadRequest = "bad_request"
	// PreviewInvalid: the draft, or the route the request was built for,
	// has errors, so there is nothing to match against yet. Message says
	// so.
	PreviewInvalid = "invalid"
)

// Preview is what a draft would do with one request, decided by the link
// model exactly as for a saved link: Compile, then Match (NFR-22).
type Preview struct {
	// Kind is one of the Preview* kinds.
	Kind string
	// Route is the index of the draft route that answered a hit; -1
	// otherwise.
	Route int
	// Destination is where a hit leads.
	Destination string
	// Values are the hit's values by variable; they are what the user
	// typed, to show and never to log (NFR-17).
	Values map[string]string
	// Reasons explain a miss: one per draft route that was tried, or only
	// the requested route's when PreviewRequest.Route was set.
	Reasons []PreviewReason
	// Message explains a bad request or an invalid draft in a sentence.
	Message string
}

// PreviewReason is why one route of a draft did not answer.
type PreviewReason struct {
	// Route is the index of the route in the draft.
	Route  int
	Reason linkmodel.Reason
}

// PreviewDraft decides what the draft d would do with req.Path if it were
// saved as it is. check is Validate's answer for the same draft: the routes
// it found errors in cannot be saved, so they take no part. Nothing is read
// from the store or the index, and nothing is written; a saved link of the
// same name plays no part either, since a save replaces it.
func PreviewDraft(d Draft, check ValidateResult, req PreviewRequest) Preview {
	none := Preview{Route: -1}
	if len(check.Routes) != len(d.Routes) {
		none.Kind, none.Message = PreviewInvalid, "This link can't be previewed until its problems are fixed."
		return none
	}
	if req.Route >= len(d.Routes) {
		none.Kind, none.Message = PreviewInvalid, "This route does not exist."
		return none
	}
	if req.Route >= 0 && linkmodel.HasErrors(check.Routes[req.Route].Problems) {
		none.Kind, none.Message = PreviewInvalid, "Fix the problems of this route to see where it leads."
		return none
	}

	// The routes that could be saved, in draft order. Position carries the
	// draft index through Compile, so a hit and a reason can say which route
	// they are about.
	link := linkmodel.Link{Name: check.Name.Value}
	for i, r := range d.Routes {
		if linkmodel.HasErrors(check.Routes[i].Problems) {
			continue
		}
		link.Routes = append(link.Routes, linkmodel.Route{
			Position: i, Pattern: r.Pattern, Destination: r.Destination, Variables: r.Variables,
		})
	}
	if len(link.Routes) == 0 {
		none.Kind, none.Message = PreviewInvalid, "Fix the problems above to see where this link leads."
		return none
	}

	segments, err := linkmodel.SplitPath(req.Path)
	switch {
	case err != nil:
		none.Kind, none.Message = PreviewBadRequest, "This go link can't be read: a % in it is not followed by two hexadecimal digits, or it is not valid text."
		return none
	case len(segments) == 0:
		none.Kind, none.Message = PreviewBadRequest, "There is no go link to try yet."
		return none
	}

	compiled := linkmodel.Compile(link)
	m, ok, reasons, err := compiled.Match(segments)
	switch {
	case err != nil:
		// Not reachable for a destination that passed validation; said
		// plainly rather than shown as a hit.
		none.Kind, none.Message = PreviewInvalid, "These values don't make a valid address on the destination's site."
		return none
	case ok:
		return Preview{Kind: PreviewHit, Route: m.Route.Position, Destination: m.Destination, Values: m.Values}
	}
	out := Preview{Kind: PreviewMissKnown, Route: -1, Reasons: make([]PreviewReason, 0, len(reasons))}
	for j, reason := range reasons {
		position := link.Routes[j].Position
		if req.Route >= 0 && position != req.Route {
			continue
		}
		out.Reasons = append(out.Reasons, PreviewReason{Route: position, Reason: reason})
	}
	return out
}
