package linkmodel

import (
	"fmt"
	"strings"
)

// Problem codes for the rules that concern a link as a whole rather than one
// pattern or destination on its own. Like the other codes they are part of
// the API.
const (
	CodeNoRoutes         = "pattern.no_routes"
	CodeNameMismatch     = "pattern.name_mismatch"
	CodeDuplicatePattern = "pattern.duplicate"
)

// DraftRoute is one route of a Draft, as the user wrote it.
type DraftRoute struct {
	Pattern     string
	Destination string
}

// Draft is a link as submitted for saving or for checking. It carries no
// name: the name is the first segment that all of its patterns share
// (PRD 5.1), so a link can never be saved under a name its routes do not
// answer to.
type Draft struct {
	Description string
	Routes      []DraftRoute
}

// DraftProblems is everything that keeps a draft from being saved, grouped
// the way a form shows it: beside the route, and within the route beside the
// field, it concerns.
type DraftProblems struct {
	// Link holds the problems that belong to no single route, such as a
	// draft without routes.
	Link []Problem
	// Routes has one entry per route of the draft, in the draft's order. An
	// entry is empty when that route is fine. Within an entry the pattern's
	// problems come before the destination's, each ordered by position.
	Routes [][]Problem
}

// Any reports whether there is at least one problem.
func (p DraftProblems) Any() bool { return p.Count() > 0 }

// Count returns the number of problems.
func (p DraftProblems) Count() int {
	n := len(p.Link)
	for _, r := range p.Routes {
		n += len(r)
	}
	return n
}

// First returns the first problem in reading order: link-level ones, then
// route by route. ok is false when there is none.
func (p DraftProblems) First() (first Problem, ok bool) {
	if len(p.Link) > 0 {
		return p.Link[0], true
	}
	for _, r := range p.Routes {
		if len(r) > 0 {
			return r[0], true
		}
	}
	return Problem{}, false
}

// ValidateDraft applies every save rule that needs no stored data and returns
// either the link to save or the problems that prevent saving. It is the one
// implementation of those rules: saving a link and checking a draft without
// saving both call it (NFR-22).
//
// The rules: every pattern and destination is valid on its own
// (ParsePattern, ValidateDestination); there is at least one route; all
// routes share one first segment, which becomes the link's name; and no two
// routes have the same pattern.
//
// The returned link has its name, each route's canonical pattern, position
// and destination, and its description with surrounding space removed. A
// destination is never altered. Whether the name is free is not decided
// here: only the store knows, at commit time.
func ValidateDraft(d Draft) (Link, DraftProblems) {
	if len(d.Routes) == 0 {
		return Link{}, DraftProblems{Link: []Problem{{
			Severity: SeverityError, Field: FieldPattern, Code: CodeNoRoutes,
			Message: "A link needs at least one route: a pattern and a destination.",
		}}}
	}

	link := Link{Description: strings.TrimSpace(d.Description)}
	problems := DraftProblems{Routes: make([][]Problem, len(d.Routes))}
	nameRoute := -1              // index of the route that set the link's name
	firstUse := map[string]int{} // shape -> index of the route that used it first

	for i, r := range d.Routes {
		pattern, patternProblems := ParsePattern(r.Pattern)
		if len(patternProblems) == 0 {
			switch {
			case nameRoute < 0:
				link.Name, nameRoute = pattern.Name(), i
			case pattern.Name() != link.Name:
				patternProblems = append(patternProblems, patternProblem(CodeNameMismatch, fmt.Sprintf(
					"This pattern starts with %q, but route %d starts with %q. Every route of a link must start with the link's name.",
					pattern.Name(), nameRoute+1, link.Name), len(pattern.Name())))
			}
			if first, used := firstUse[pattern.Shape()]; used {
				patternProblems = append(patternProblems, patternProblem(CodeDuplicatePattern, fmt.Sprintf(
					"This pattern is the same as the pattern of route %d.", first+1), len(r.Pattern)))
			} else {
				firstUse[pattern.Shape()] = i
			}
			link.Routes = append(link.Routes, Route{
				Position: i, Pattern: pattern.Canonical(), Destination: r.Destination,
			})
		}
		problems.Routes[i] = append(patternProblems, ValidateDestination(r.Destination)...)
	}

	if problems.Any() {
		return Link{}, problems
	}
	return link, DraftProblems{}
}

// patternProblem returns an error on a pattern's first end bytes.
func patternProblem(code, message string, end int) Problem {
	return Problem{
		Severity: SeverityError, Field: FieldPattern,
		Code: code, Message: message, Start: 0, End: end,
	}
}
