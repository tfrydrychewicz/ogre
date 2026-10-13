package linkmodel

import (
	"slices"
	"strings"
	"testing"
)

// draft builds a Draft from pattern, destination pairs.
func draft(pairs ...string) Draft {
	var d Draft
	for i := 0; i+1 < len(pairs); i += 2 {
		d.Routes = append(d.Routes, DraftRoute{Pattern: pairs[i], Destination: pairs[i+1]})
	}
	return d
}

func TestValidateDraftBuildsTheLink(t *testing.T) {
	in := Draft{
		Description: "  GitHub shortcuts \n",
		Routes: []DraftRoute{
			{Pattern: "GH", Destination: "https://github.com/acme"},
			{Pattern: "Gh/New", Destination: "https://GitHub.com/Organizations/Acme/new?Tab=A%2Fb#Frag"},
			{Pattern: "gh/settings/billing", Destination: "https://example.com/żółć"},
		},
	}
	got, problems := ValidateDraft(in)
	if problems.Any() {
		t.Fatalf("ValidateDraft problems = %+v, want none", problems)
	}
	if problems.Link != nil || problems.Routes != nil {
		t.Errorf("a valid draft returned non-zero problems %+v", problems)
	}
	if got.Name != "gh" {
		t.Errorf("Name = %q, want %q (the shared first segment, lowercase)", got.Name, "gh")
	}
	if got.Description != "GitHub shortcuts" {
		t.Errorf("Description = %q, want it trimmed", got.Description)
	}
	if got.ID != 0 || got.OwnerID != 0 || got.Version != 0 || !got.CreatedAt.IsZero() {
		t.Errorf("ValidateDraft set fields that belong to the store or the use case: %+v", got)
	}
	want := []Route{
		{Position: 0, Pattern: "gh", Destination: "https://github.com/acme"},
		// Patterns are saved lowercase; a destination is never altered, not
		// in case, not in encoding, not by dropping its fragment.
		{Position: 1, Pattern: "gh/new", Destination: "https://GitHub.com/Organizations/Acme/new?Tab=A%2Fb#Frag"},
		{Position: 2, Pattern: "gh/settings/billing", Destination: "https://example.com/żółć"},
	}
	if !slices.Equal(got.Routes, want) {
		t.Errorf("Routes =\n  %+v\nwant\n  %+v", got.Routes, want)
	}
	// The caller's draft is left as it was.
	if in.Routes[0].Pattern != "GH" || in.Routes[1].Pattern != "Gh/New" || in.Description != "  GitHub shortcuts \n" {
		t.Errorf("ValidateDraft changed the draft: %+v", in)
	}
}

func TestValidateDraftProblems(t *testing.T) {
	type want struct {
		field, code string
		start, end  int
		prefix      string // what the message must begin with
	}
	none := []want(nil)
	const ok = "https://example.com"
	tests := []struct {
		name   string
		draft  Draft
		link   []want
		routes [][]want // one entry per route of the draft
	}{
		{"no routes", Draft{Description: "nothing"},
			[]want{{"pattern", CodeNoRoutes, 0, 0, "A link needs"}}, nil},
		{"empty pattern and destination", draft("", ""), nil, [][]want{{
			{"pattern", CodePatternEmpty, 0, 0, "Enter a pattern"},
			{"destination", CodeDestinationEmpty, 0, 0, "Enter the URL"}}}},
		{"bad pattern", draft("on call", ok), nil, [][]want{{
			{"pattern", CodePatternBadCharacter, 2, 3, `' ' can't`}}}},
		{"leading hyphen", draft("-oncall", ok), nil, [][]want{{
			{"pattern", CodePatternBadStart, 0, 1, "A segment must"}}}},
		{"variable", draft("gh/{repo}", ok), nil, [][]want{{
			{"pattern", CodePatternVariablesUnsupported, 3, 9, "Variables"}}}},
		{"reserved name", draft("favicon.ico", ok), nil, [][]want{{
			{"pattern", CodePatternReservedName, 0, 11, `"favicon.ico" can't`}}}},
		{"empty segment", draft("gh//new", ok), nil, [][]want{{
			{"pattern", CodePatternEmptySegment, 3, 4, "A pattern can't"}}}},
		{"relative destination", draft("roadmap", "example.com/roadmap"), nil, [][]want{{
			{"destination", CodeDestinationNotAbsolute, 0, 19, "A destination must"}}}},
		{"javascript destination", draft("roadmap", "javascript:alert(1)"), nil, [][]want{{
			{"destination", CodeDestinationScheme, 0, 10, "A destination must"}}}},
		{"placeholder in the destination", draft("gh", "https://github.com/{repo}"), nil, [][]want{{
			{"destination", CodeDestinationPlaceholderUnsupported, 19, 25, "Placeholders"}}}},
		// Within a route the pattern's problems come first.
		{"both fields of one route", draft("a b", "ftp://x"), nil, [][]want{{
			{"pattern", CodePatternBadCharacter, 1, 2, `' ' can't`},
			{"destination", CodeDestinationScheme, 0, 3, "A destination must"}}}},

		// Several routes: every problem is collected under its own route,
		// and a route without problems still has its (empty) entry.
		{"problems in several routes", draft("gh", "nope", "gh/-x", ok, "gh/ok", ok, "gh/new", "ftp://x"), nil, [][]want{
			{{"destination", CodeDestinationNotAbsolute, 0, 4, "A destination must"}},
			{{"pattern", CodePatternBadStart, 3, 4, "A segment must"}},
			none,
			{{"destination", CodeDestinationScheme, 0, 3, "A destination must"}}}},

		// Rules across routes.
		{"different names", draft("gh", ok, "github/new", ok), nil, [][]want{none, {
			{"pattern", CodeNameMismatch, 0, 6, `This pattern starts with "github", but route 1 starts with "gh".`}}}},
		{"different names, four routes", draft("gh", ok, "gl", ok, "gh/new", ok, "bb/x", ok), nil, [][]want{none,
			{{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "gl"`}},
			none,
			{{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "bb"`}}}},
		// Q5: a separator is part of the name, so these are two names.
		{"names differing only in a separator", draft("on-call", ok, "on_call/x", ok), nil, [][]want{none, {
			{"pattern", CodeNameMismatch, 0, 7, `This pattern starts with "on_call"`}}}},
		{"names differing only in case are one name", draft("On-Call", ok, "on-call/x", ok), nil, nil},
		{"name taken from the first valid route", draft("{x}", ok, "gh", ok, "gl", ok), nil, [][]want{
			{{"pattern", CodePatternVariablesUnsupported, 0, 3, "Variables"}},
			none,
			{{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "gl", but route 2 starts with "gh".`}}}},
		{"duplicate pattern", draft("gh", ok, "gh", "https://other.example.com"), nil, [][]want{none, {
			{"pattern", CodeDuplicatePattern, 0, 2, "This pattern is the same as the pattern of route 1."}}}},
		{"duplicate ignoring case", draft("gh/new", ok, "gh", ok, "GH/New", ok), nil, [][]want{none, none, {
			{"pattern", CodeDuplicatePattern, 0, 6, "This pattern is the same as the pattern of route 1."}}}},
		{"duplicated twice", draft("gh", ok, "gh", ok, "gh", ok), nil, [][]want{none,
			{{"pattern", CodeDuplicatePattern, 0, 2, "This pattern is the same as the pattern of route 1."}},
			{{"pattern", CodeDuplicatePattern, 0, 2, "This pattern is the same as the pattern of route 1."}}}},
		{"other name and a duplicate of it", draft("gh", ok, "gl", ok, "gl", ok), nil, [][]want{none,
			{{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "gl"`}},
			{{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "gl"`},
				{"pattern", CodeDuplicatePattern, 0, 2, "This pattern is the same as the pattern of route 2."}}}},
	}
	check := func(t *testing.T, where string, got []Problem, wants []want) {
		t.Helper()
		if len(got) != len(wants) {
			t.Errorf("%s: problems =\n  %+v\nwant %d:\n  %+v", where, got, len(wants), wants)
			return
		}
		for i, w := range wants {
			p := got[i]
			if p.Severity != SeverityError || p.Field != w.field || p.Code != w.code ||
				p.Start != w.start || p.End != w.end || !strings.HasPrefix(p.Message, w.prefix) {
				t.Errorf("%s: problem %d = %+v\nwant %+v", where, i, p, w)
			}
			// The position in Routes says which route; the text must not.
			if strings.HasPrefix(p.Message, "Route ") {
				t.Errorf("%s: message %q is labeled with its route", where, p.Message)
			}
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link, problems := ValidateDraft(tt.draft)
			if tt.link == nil && tt.routes == nil {
				if problems.Any() || link.Name == "" {
					t.Fatalf("ValidateDraft = %+v, %+v; want a link and no problems", link, problems)
				}
				return
			}
			if link.Name != "" || link.Routes != nil || link.Description != "" {
				t.Errorf("ValidateDraft returned link %+v with problems", link)
			}
			check(t, "link", problems.Link, tt.link)
			if len(problems.Routes) != len(tt.draft.Routes) {
				t.Fatalf("problems has %d route entries for a draft of %d routes", len(problems.Routes), len(tt.draft.Routes))
			}
			total := len(tt.link)
			for i := range tt.draft.Routes {
				check(t, "route "+string(rune('1'+i)), problems.Routes[i], tt.routes[i])
				total += len(tt.routes[i])
			}
			if problems.Count() != total || !problems.Any() {
				t.Errorf("Count() = %d, Any() = %v; want %d problems", problems.Count(), problems.Any(), total)
			}
			first, ok := problems.First()
			if !ok || first.Message == "" {
				t.Errorf("First() = %+v, %v; want the first problem", first, ok)
			}
		})
	}
}

func TestDraftProblemsFirst(t *testing.T) {
	p := func(msg string) Problem { return Problem{Message: msg} }
	tests := []struct {
		name     string
		problems DraftProblems
		want     string
		count    int
	}{
		{"none", DraftProblems{}, "", 0},
		{"none, with empty route entries", DraftProblems{Routes: [][]Problem{nil, {}}}, "", 0},
		{"link-level first", DraftProblems{Link: []Problem{p("link")}, Routes: [][]Problem{{p("route")}}}, "link", 2},
		{"first route with a problem", DraftProblems{Routes: [][]Problem{nil, {p("second"), p("x")}, {p("third")}}}, "second", 3},
	}
	for _, tt := range tests {
		got, ok := tt.problems.First()
		if got.Message != tt.want || ok != (tt.want != "") {
			t.Errorf("%s: First() = %q, %v; want %q", tt.name, got.Message, ok, tt.want)
		}
		if tt.problems.Count() != tt.count || tt.problems.Any() != (tt.count > 0) {
			t.Errorf("%s: Count() = %d, Any() = %v; want %d", tt.name, tt.problems.Count(), tt.problems.Any(), tt.count)
		}
	}
}
