package linkmodel

// VariableInfo is everything there is to show about one variable of a
// saved route: what the pattern says about it (its name, its type, what the
// type accepts) together with what is stored beside the pattern (a
// description and an example).
type VariableInfo struct {
	Name string
	// Type is the type's name: "string", "int", "number" or "regex".
	Type string
	// Accepts says what the variable accepts, in words (VarType.Describe).
	Accepts     string
	Description string
	Example     string
}

// RouteVariables returns the variables of a saved route, in pattern order.
//
// It exists so that nothing outside this package has to parse a pattern to
// show a link (NFR-22): the API and the pages ask here. The pattern is read
// the way Compile reads it, so a route that resolves can always be shown. A
// route whose pattern cannot be read has no variables to show, and nil is
// returned.
//
// The description and example come from Route.Variables by name; a variable
// the route stores nothing about has both empty.
func RouteVariables(r Route) []VariableInfo {
	pattern, ok := StoredPattern(r.Pattern)
	if !ok || len(pattern.variables) == 0 {
		return nil
	}
	infos := make([]VariableInfo, 0, len(pattern.variables))
	for _, seg := range pattern.segments {
		if seg.Type == nil {
			continue
		}
		info := VariableInfo{Name: seg.Variable, Type: seg.Type.Name(), Accepts: seg.Type.Describe()}
		for _, meta := range r.Variables {
			if meta.Name == seg.Variable {
				info.Description, info.Example = meta.Description, meta.Example
				break
			}
		}
		infos = append(infos, info)
	}
	return infos
}
