package app

import (
	"context"
	"slices"

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
			res.Routes[i].Samples = trySamples(req.Draft.Routes[i], req.Samples[i], a.rules)
		}
	}

	res.Valid = res.Name.Available && !linkmodel.HasErrors(res.Problems)
	for _, r := range res.Routes {
		res.Valid = res.Valid && !linkmodel.HasErrors(r.Problems)
	}
	return res
}

// trySamples says, for each sample value, whether the route would accept it
// for that variable. The answer is the link model's (linkmodel.Accepts): the
// variable's type, and the rule that a variable used in the destination's
// path refuses "." and ".." (NFR-10). It is what Match would decide for that
// segment once the route is saved.
//
// A pattern that is not valid yet is asked about the variables that can
// already be read (linkmodel.PatternSoFar), so a form can show what a value
// would do while the rest of the pattern is still being typed. A destination
// that cannot be read against those variables leaves only the types to
// decide. Samples for a variable that cannot be read, or that the pattern
// does not declare, are left out of the result.
func trySamples(r DraftRoute, samples map[string][]string, rules linkmodel.ValidateOptions) map[string]map[string]bool {
	_, pattern := linkmodel.PatternSoFar(r.Pattern)
	template, problems := linkmodel.ParseDestination(r.Destination, pattern, rules)
	if linkmodel.HasErrors(problems) {
		template = linkmodel.Template{}
	}
	result := make(map[string]map[string]bool, len(samples))
	for variable, values := range samples {
		if !slices.Contains(pattern.Variables(), variable) {
			continue
		}
		accepted := make(map[string]bool, len(values))
		for _, value := range values {
			accepted[value], _ = linkmodel.Accepts(pattern, template, variable, value)
		}
		result[variable] = accepted
	}
	return result
}
