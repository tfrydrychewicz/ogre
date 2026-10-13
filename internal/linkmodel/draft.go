package linkmodel

import (
	"fmt"
	"strings"
)

// MaxRoutes is the most routes one link can have (PRD 5.2).
const MaxRoutes = 20

// DraftRoute is one route of a Draft, as the user wrote it.
type DraftRoute struct {
	// ID is 0 for a new route. Otherwise it names the stored route this one
	// replaces, so an edit keeps the route's identity (and with it, later, its
	// usage counts) instead of deleting and re-creating it.
	ID          int64
	Pattern     string
	Destination string
	// Variables optionally describes the pattern's variables. An entry for a
	// name the pattern does not declare is a problem; a variable without an
	// entry gets an empty description and example.
	Variables []VariableMeta
}

// Draft is a link as submitted for saving or for checking. It carries no
// name: the name is the first segment that all of its patterns share
// (PRD 5.1), so a link can never be saved under a name its routes do not
// answer to. Renaming a link is therefore nothing special: it is a draft
// whose patterns begin with another name.
type Draft struct {
	Description string
	Routes      []DraftRoute
}

// DraftProblems is what ValidateDraft found in a draft, grouped the way a
// form shows it: beside the route, and within the route beside the field, it
// concerns. It holds errors, which block saving, and warnings, which do not;
// ask HasErrors, not whether it is empty.
type DraftProblems struct {
	// Link holds the problems that belong to no single route, such as a
	// draft without routes.
	Link []Problem
	// Routes has one entry per route of the draft, in the draft's order. An
	// entry is empty when that route is fine. Within an entry the pattern's
	// problems come before the destination's, each ordered by position.
	Routes [][]Problem
	// Tokens has one entry per route of the draft: the segments of its
	// pattern that could be read (see Token). It is filled for valid and
	// invalid drafts alike; the validation endpoint returns it so that the
	// browser never parses a pattern itself.
	Tokens [][]Token
}

// HasErrors reports whether anything blocks saving.
func (p DraftProblems) HasErrors() bool {
	if HasErrors(p.Link) {
		return true
	}
	for _, r := range p.Routes {
		if HasErrors(r) {
			return true
		}
	}
	return false
}

// Count returns the number of problems, errors and warnings together.
func (p DraftProblems) Count() int {
	n := len(p.Link)
	for _, r := range p.Routes {
		n += len(r)
	}
	return n
}

// First returns the first error in reading order: link-level ones, then
// route by route. Warnings are passed over. ok is false when there is no
// error.
func (p DraftProblems) First() (first Problem, ok bool) {
	for _, group := range append([][]Problem{p.Link}, p.Routes...) {
		for _, problem := range group {
			if problem.Severity != SeverityWarning {
				return problem, true
			}
		}
	}
	return Problem{}, false
}

// ValidateDraft applies every save rule that needs no stored data and
// returns the link to save together with what it found. It is the one
// implementation of those rules: saving a link and checking a draft without
// saving both call it (NFR-22). opts carries what the rules need to know
// about the Directory; without it the rule against destinations on the
// Directory itself is not applied.
//
// The rules: at least one route and at most MaxRoutes (a draft with more is
// refused without being read any further); every pattern is
// valid (ParsePattern); every destination is valid for its pattern
// (ParseDestination); all routes share one first segment, which becomes the
// link's name; no two routes have the same shape, which is the same pattern
// once variable names are ignored (PRD 5.5); no stored route is claimed by
// two routes; the regular expressions of all routes together stay within
// MaxLinkRegexProgram; and the descriptions of variables name variables that
// exist.
//
// When problems.HasErrors() is false the returned link is ready to save: it
// has its name, and for each route the ID the draft gave it, its position,
// canonical pattern, destination and one VariableMeta per variable of the
// pattern, in pattern order. The link's description and each variable's
// description have surrounding space removed. A destination and an example
// are never altered. Warnings may accompany such a link. When there are
// errors the link is the zero Link.
//
// Whether the name is free, and whether a route ID belongs to the link being
// edited, are not decided here: only the store knows.
func ValidateDraft(d Draft, opts ...ValidateOptions) (Link, DraftProblems) {
	if len(d.Routes) == 0 {
		return Link{}, DraftProblems{Link: []Problem{linkProblem(CodeNoRoutes,
			"A link needs at least one route: a pattern and a destination.")}}
	}

	if len(d.Routes) > MaxRoutes {
		// Nothing else is looked at. A request body has room for thousands
		// of routes, and reading each of them, regular expressions included,
		// would cost seconds for a draft that is already refused. Routes and
		// Tokens stay empty: there is no per-route answer.
		return Link{}, DraftProblems{Link: []Problem{linkProblem(CodeTooManyRoutes, fmt.Sprintf(
			"A link can have at most %d routes; this one has %d.", MaxRoutes, len(d.Routes)))}}
	}

	link := Link{Description: strings.TrimSpace(d.Description)}
	problems := DraftProblems{
		Routes: make([][]Problem, len(d.Routes)),
		Tokens: make([][]Token, len(d.Routes)),
	}

	var (
		nameRoute  = -1                            // index of the route that set the link's name
		canonical  = make([]string, len(d.Routes)) // canonical pattern of each valid route
		firstShape = map[string]int{}              // shape -> index of the route that used it first
		firstID    = map[int64]int{}               // stored route ID -> index of the route that claimed it first
		program    = 0                             // instructions of all regular expressions together
	)

	for i, r := range d.Routes {
		pattern, tokens, found := scanPattern(r.Pattern, true)
		problems.Tokens[i] = tokens
		whole := len(r.Pattern)

		if r.ID != 0 {
			if first, claimed := firstID[r.ID]; claimed {
				problems.Link = append(problems.Link, linkProblem(CodeDuplicateRouteID, fmt.Sprintf(
					"Routes %d and %d both replace the same saved route. Each saved route can be kept once.",
					first+1, i+1)))
			} else {
				firstID[r.ID] = i
			}
		}

		var variables []VariableMeta
		if len(found) == 0 {
			switch {
			case nameRoute < 0:
				link.Name, nameRoute = pattern.Name(), i
			case pattern.Name() != link.Name:
				found = append(found, patternProblem(CodeNameMismatch, fmt.Sprintf(
					"This pattern starts with %q, but route %d starts with %q. Every route of a link must start with the link's name.",
					pattern.Name(), nameRoute+1, link.Name), 0, len(pattern.Name())))
			}
			canonical[i] = pattern.String()
			program += regexProgram(pattern)
			if first, used := firstShape[pattern.Shape()]; !used {
				firstShape[pattern.Shape()] = i
			} else if canonical[first] == canonical[i] {
				found = append(found, patternProblem(CodeDuplicatePattern, fmt.Sprintf(
					"This pattern is the same as the pattern of route %d.", first+1), 0, whole))
			} else {
				found = append(found, patternProblem(CodeSameShape, fmt.Sprintf(
					"This pattern has the same shape as route %d, %s: they differ only in the names of their variables, so they would answer the same requests.",
					first+1, canonical[first]), 0, whole))
			}
			var metaProblems []Problem
			variables, metaProblems = variableMeta(pattern, r.Variables, whole)
			found = append(found, metaProblems...)
		}

		// The destination is checked against the pattern's variables only
		// when the pattern could be read (the zero Pattern says it could
		// not).
		template, destinationProblems := ParseDestination(r.Destination, pattern, opts...)
		found = byPosition(append(found, exampleWarnings(pattern, template, variables)...))
		problems.Routes[i] = append(found, destinationProblems...)

		link.Routes = append(link.Routes, Route{
			ID: r.ID, Position: i, Pattern: pattern.String(), Destination: r.Destination, Variables: variables,
		})
	}

	if program > MaxLinkRegexProgram {
		// Each expression passed its own limit; together they are more than
		// one request should be able to set in motion (NFR-8).
		problems.Link = append(problems.Link, linkProblem(CodePatternRegexTooComplex,
			"The regular expressions of this link are too complex taken together. "+
				"Use simpler ones, usually by lowering large repeat counts such as {200}, or use fewer of them."))
	}

	if problems.HasErrors() {
		return Link{}, problems
	}
	return link, problems
}

// variableMeta lines the descriptions sent with a route up with the
// pattern's variables: one entry per variable, in pattern order, empty where
// nothing was sent. An entry for a name the pattern does not declare, or a
// second entry for the same name, is a problem; silently dropping it would
// lose something the user wrote.
func variableMeta(p Pattern, sent []VariableMeta, patternEnd int) ([]VariableMeta, []Problem) {
	var problems []Problem
	index := map[string]int{}
	var result []VariableMeta
	for i, name := range p.variables {
		index[name] = i
		result = append(result, VariableMeta{Name: name})
	}
	filled := map[string]bool{}
	for _, meta := range sent {
		i, declared := index[meta.Name]
		switch {
		case !declared:
			problems = append(problems, patternProblem(CodeUnknownVariableMeta, fmt.Sprintf(
				"A description was sent for a variable named %s, but the pattern has no such variable.",
				quoteForMessage(meta.Name)), patternEnd, patternEnd))
		case filled[meta.Name]:
			problems = append(problems, patternProblem(CodeDuplicateVariableMeta, fmt.Sprintf(
				"Two descriptions were sent for the variable %s.", meta.Name), patternEnd, patternEnd))
		default:
			filled[meta.Name] = true
			result[i].Description = strings.TrimSpace(meta.Description)
			result[i].Example = meta.Example
		}
	}
	return result, problems
}

// exampleWarnings warns about every example that its own variable would not
// accept. An example is shown as a hint for what to type, so a wrong one
// misleads; it does no harm to matching, which is why it is only a warning.
func exampleWarnings(p Pattern, t Template, variables []VariableMeta) []Problem {
	var warnings []Problem
	for _, meta := range variables {
		if meta.Example == "" {
			continue
		}
		if accepted, _ := Accepts(p, t, meta.Name, meta.Example); accepted {
			continue
		}
		seg, _ := p.variable(meta.Name)
		warnings = append(warnings, Problem{
			Severity: SeverityWarning, Field: FieldPattern, Code: CodeExampleRejected,
			Message: fmt.Sprintf("The example %s for %s is not a value it accepts. It accepts %s.",
				quoteForMessage(meta.Example), meta.Name, seg.Type.Describe()),
			Start: seg.Start, End: seg.End,
		})
	}
	return warnings
}

// linkProblem returns an error that belongs to the link as a whole.
func linkProblem(code, message string) Problem {
	return Problem{Severity: SeverityError, Field: FieldLink, Code: code, Message: message}
}

// patternProblem returns an error on the bytes [start, end) of a pattern.
func patternProblem(code, message string, start, end int) Problem {
	return Problem{
		Severity: SeverityError, Field: FieldPattern,
		Code: code, Message: message, Start: start, End: end,
	}
}
