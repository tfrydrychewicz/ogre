package linkmodel

import (
	"reflect"
	"strings"
	"testing"
)

func TestRouteVariables(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  []VariableInfo
	}{
		{"no variables", Route{Pattern: "gh/new"}, nil},
		{"plan 3.5", Route{Pattern: "gh/{repo}/pr/{num:int}"}, []VariableInfo{
			{Name: "repo", Type: "string", Accepts: "any text"},
			{Name: "num", Type: "int", Accepts: "a whole number"},
		}},
		{"every type", Route{Pattern: "x/{a}/{b:int}/{c:number}/{d:regex([A-Z]+)}"}, []VariableInfo{
			{Name: "a", Type: "string", Accepts: "any text"},
			{Name: "b", Type: "int", Accepts: "a whole number"},
			{Name: "c", Type: "number", Accepts: "a number such as 42 or 3.5"},
			{Name: "d", Type: "regex", Accepts: "text matching [A-Z]+"},
		}},
		{"descriptions by name, in pattern order", Route{Pattern: "reports/{month:int}/{day:int}", Variables: []VariableMeta{
			{Name: "day", Description: "Day of the month", Example: "13"},
			{Name: "month", Description: "Month, 1 to 12", Example: "10"},
			{Name: "year", Description: "left over from an older pattern"},
		}}, []VariableInfo{
			{Name: "month", Type: "int", Accepts: "a whole number", Description: "Month, 1 to 12", Example: "10"},
			{Name: "day", Type: "int", Accepts: "a whole number", Description: "Day of the month", Example: "13"},
		}},
		{"nothing stored for one variable", Route{Pattern: "x/{a}/{b}", Variables: []VariableMeta{{Name: "b", Example: "7"}}}, []VariableInfo{
			{Name: "a", Type: "string", Accepts: "any text"},
			{Name: "b", Type: "string", Accepts: "any text", Example: "7"},
		}},
		{"a pattern that cannot be read", Route{Pattern: "x/{a", Variables: []VariableMeta{{Name: "a"}}}, nil},
		{"empty pattern", Route{}, nil},
	}
	for _, tt := range tests {
		if got := RouteVariables(tt.route); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: RouteVariables =\n  %+v\nwant\n  %+v", tt.name, got, tt.want)
		}
	}

	// What ValidateDraft produces is what RouteVariables shows.
	link, problems := ValidateDraft(Draft{Routes: []DraftRoute{{
		Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://example.com/{repo}/{num}",
		Variables: []VariableMeta{{Name: "num", Description: "Pull request", Example: "42"}},
	}}})
	if problems.Count() != 0 {
		t.Fatal(problems)
	}
	infos := RouteVariables(link.Routes[0])
	if len(infos) != len(link.Routes[0].Variables) {
		t.Fatalf("RouteVariables = %+v for the stored %+v", infos, link.Routes[0].Variables)
	}
	for i, meta := range link.Routes[0].Variables {
		if infos[i].Name != meta.Name || infos[i].Description != meta.Description || infos[i].Example != meta.Example {
			t.Errorf("variable %d = %+v, stored %+v", i, infos[i], meta)
		}
	}

	// A route that resolves can be shown, even where saving it again would
	// be refused (see TestStoredPatternIsNotHeldToProgramSize).
	stored := Route{Pattern: "x/{v:regex((?:" + strings.Repeat("a", 120) + "){17})}"}
	if infos := RouteVariables(stored); len(infos) != 1 || infos[0].Type != "regex" {
		t.Errorf("RouteVariables of a stored route = %+v", infos)
	}
}
