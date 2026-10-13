package linkmodel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
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
			{ID: 31, Pattern: "Gh/New", Destination: "https://GitHub.com/Organizations/Acme/new?Tab=A%2Fb#Frag"},
			{Pattern: "gh/settings/billing", Destination: "https://example.com/żółć"},
			{ID: 7, Pattern: "GH/{Repo}/PR/{num:INT}", Destination: "https://github.com/acme/{Repo}/pull/{num}",
				Variables: []VariableMeta{
					{Name: "num", Description: "  Pull request number ", Example: "42"},
					{Name: "Repo", Description: "Repository", Example: " api-server"},
				}},
			{Pattern: "gh/{id:string}", Destination: "https://github.com/acme/{id}"},
		},
	}
	got, problems := ValidateDraft(in)
	if problems.Count() != 0 || problems.HasErrors() {
		t.Fatalf("ValidateDraft problems = %+v, want none", problems)
	}
	if problems.Link != nil || len(problems.Routes) != 5 || len(problems.Tokens) != 5 {
		t.Errorf("a valid draft returned %+v; want an entry per route and nothing in them", problems)
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
		// Patterns are saved in canonical form; a destination is never
		// altered, not in case, not in encoding, not by dropping its
		// fragment. A route keeps the ID the draft gave it.
		{ID: 31, Position: 1, Pattern: "gh/new", Destination: "https://GitHub.com/Organizations/Acme/new?Tab=A%2Fb#Frag"},
		{Position: 2, Pattern: "gh/settings/billing", Destination: "https://example.com/żółć"},
		// One entry per variable, in pattern order whatever order they were
		// sent in. A description is trimmed; an example is a value, and
		// values are never changed.
		{ID: 7, Position: 3, Pattern: "gh/{Repo}/pr/{num:int}", Destination: "https://github.com/acme/{Repo}/pull/{num}",
			Variables: []VariableMeta{
				{Name: "Repo", Description: "Repository", Example: " api-server"},
				{Name: "num", Description: "Pull request number", Example: "42"},
			}},
		// Nothing was sent for this variable; it still has its entry.
		{Position: 4, Pattern: "gh/{id}", Destination: "https://github.com/acme/{id}",
			Variables: []VariableMeta{{Name: "id"}}},
	}
	if !reflect.DeepEqual(got.Routes, want) {
		t.Errorf("Routes =\n  %+v\nwant\n  %+v", got.Routes, want)
	}
	// The caller's draft is left as it was.
	if in.Routes[0].Pattern != "GH" || in.Routes[1].Pattern != "Gh/New" || in.Description != "  GitHub shortcuts \n" ||
		in.Routes[3].Variables[0].Description != "  Pull request number " {
		t.Errorf("ValidateDraft changed the draft: %+v", in)
	}
	// What was validated is what resolves.
	m, ok, _, err := Compile(got).Match([]string{"gh", "api-server", "pr", "42"})
	if !ok || err != nil || m.Route.ID != 7 || m.Destination != "https://github.com/acme/api-server/pull/42" {
		t.Errorf("the validated link resolved to %+v, %v, %v", m, ok, err)
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
			[]want{{"link", CodeNoRoutes, 0, 0, "A link needs"}}, nil},
		{"empty pattern and destination", draft("", ""), nil, [][]want{{
			{"pattern", CodePatternEmpty, 0, 0, "Enter a pattern"},
			{"destination", CodeDestinationEmpty, 0, 0, "Enter the URL"}}}},
		{"bad pattern", draft("on call", ok), nil, [][]want{{
			{"pattern", CodePatternBadCharacter, 2, 3, `' ' can't`}}}},
		{"leading hyphen", draft("-oncall", ok), nil, [][]want{{
			{"pattern", CodePatternBadStart, 0, 1, "A segment must"}}}},
		{"unclosed variable", draft("gh/{repo", ok), nil, [][]want{{
			{"pattern", CodePatternUnclosedVariable, 3, 8, "This variable is missing"}}}},
		{"reserved name", draft("favicon.ico", ok), nil, [][]want{{
			{"pattern", CodePatternReservedName, 0, 11, `"favicon.ico" can't`}}}},
		{"empty segment", draft("gh//new", ok), nil, [][]want{{
			{"pattern", CodePatternEmptySegment, 3, 4, "A pattern can't"}}}},
		{"relative destination", draft("roadmap", "example.com/roadmap"), nil, [][]want{{
			{"destination", CodeDestinationNotAbsolute, 0, 19, "A destination must"}}}},
		{"javascript destination", draft("roadmap", "javascript:alert(1)"), nil, [][]want{{
			{"destination", CodeDestinationScheme, 0, 10, "A destination must"}}}},
		{"credentials", draft("roadmap", "https://example.com@evil.example/"), nil, [][]want{{
			{"destination", CodeDestinationCredentials, 8, 20, "A destination can't contain a user name"}}}},
		{"go link as a destination", draft("roadmap", "http://go/other"), nil, [][]want{{
			{"destination", CodeDestinationGoLink, 7, 9, "A destination can't be another go link"}}}},
		{"undeclared placeholder", draft("gh", "https://github.com/{repo}"), nil, [][]want{{
			{"destination", CodeDestinationUndeclaredVariable, 19, 25, "The pattern has no variable {repo}"}}}},
		{"placeholder in the host", draft("wiki/{team}", "https://{team}.example.com/"), nil, [][]want{{
			{"destination", CodeDestinationPlaceholderInHost, 8, 14, "{team} can't be used here"}}}},
		// Within a route the pattern's problems come first.
		{"both fields of one route", draft("a b", "ftp://x"), nil, [][]want{{
			{"pattern", CodePatternBadCharacter, 1, 2, `' ' can't`},
			{"destination", CodeDestinationScheme, 0, 3, "A destination must"}}}},
		// When the pattern cannot be read, the destination's placeholders
		// are checked for their form only: what is declared is unknown.
		{"bad pattern, placeholder not judged", draft("gh/{repo", "https://github.com/{repo}/{other}"), nil, [][]want{{
			{"pattern", CodePatternUnclosedVariable, 3, 8, "This variable"}}}},
		{"bad pattern, bad placeholder still reported", draft("gh/{repo", "https://github.com/{repo"), nil, [][]want{{
			{"pattern", CodePatternUnclosedVariable, 3, 8, "This variable"},
			{"destination", CodeDestinationUnclosedPlaceholder, 19, 24, "This placeholder"}}}},

		// Several routes: every problem is collected under its own route,
		// and a route without problems still has its (empty) entry.
		{"problems in several routes", draft("gh", "nope", "gh/-x", ok, "gh/ok", ok, "gh/new", "ftp://x"), nil, [][]want{
			{{"destination", CodeDestinationNotAbsolute, 0, 4, "A destination must"}},
			{{"pattern", CodePatternBadStart, 3, 4, "A segment must"}},
			none,
			{{"destination", CodeDestinationScheme, 0, 3, "A destination must"}}}},

		// All routes share one name.
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
			{{"pattern", CodePatternVariableFirst, 0, 3, "A pattern must begin"}},
			none,
			{{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "gl", but route 2 starts with "gh".`}}}},
		{"different names with variables", draft("gh/{repo}", "https://x.example/{repo}", "gl/{repo}", "https://x.example/{repo}"), nil, [][]want{none, {
			{"pattern", CodeNameMismatch, 0, 2, `This pattern starts with "gl"`}}}},

		// One shape once (PRD 5.5).
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
		{"duplicate with variables", draft("gh/{repo}", "https://x.example/{repo}", "GH/{repo:string}", "https://y.example/{repo}"), nil, [][]want{none, {
			{"pattern", CodeDuplicatePattern, 0, 16, "This pattern is the same as the pattern of route 1."}}}},
		{"PRD 5.7: gh/{name} has the shape of gh/{repo}", draft("gh/{repo}", "https://x.example/{repo}", "gh/{name}", "https://y.example/{name}"), nil, [][]want{none, {
			{"pattern", CodeSameShape, 0, 9, "This pattern has the same shape as route 1, gh/{repo}: they differ only in the names of their variables"}}}},
		{"same shape, typed", draft("gh/{a}/pr/{n:int}", "https://x.example/{a}/{n}", "gh", ok, "gh/{b}/pr/{m:int}", "https://x.example/{b}/{m}"), nil, [][]want{none, none, {
			{"pattern", CodeSameShape, 0, 17, "This pattern has the same shape as route 1, gh/{a}/pr/{n:int}"}}}},
		{"same shape, same expression", draft("j/{k:regex([A-Z]+)}", "https://x.example/{k}", "j/{key:regex([A-Z]+)}", "https://x.example/{key}"), nil, [][]want{none, {
			{"pattern", CodeSameShape, 0, 21, "This pattern has the same shape as route 1, j/{k:regex([A-Z]+)}"}}}},
		{"same shape, variable names differing in case", draft("gh/{repo}", "https://x.example/{repo}", "gh/{Repo}", "https://x.example/{Repo}"), nil, [][]want{none, {
			{"pattern", CodeSameShape, 0, 9, "This pattern has the same shape as route 1, gh/{repo}"}}}},
		// PRD 5.7: gh/{id:int} does not collide with gh/{repo}.
		{"a type makes another shape", draft("gh/{repo}", "https://x.example/{repo}", "gh/{id:int}", "https://x.example/{id}"), nil, nil},
		{"another expression makes another shape", draft("j/{k:regex([A-Z]+)}", "https://x.example/{k}", "j/{k:regex([a-z]+)}", "https://x.example/{k}"), nil, nil},
		{"a literal makes another shape", draft("gh/{repo}", "https://x.example/{repo}", "gh/new", ok), nil, nil},

		// Descriptions of variables.
		{"description for an unknown variable", Draft{Routes: []DraftRoute{{Pattern: "gh/{repo}", Destination: "https://x.example/{repo}",
			Variables: []VariableMeta{{Name: "repo"}, {Name: "num", Description: "x"}}}}}, nil, [][]want{{
			{"pattern", CodeUnknownVariableMeta, 9, 9, `A description was sent for a variable named "num"`}}}},
		{"description for a variable in another case", Draft{Routes: []DraftRoute{{Pattern: "gh/{repo}", Destination: "https://x.example/{repo}",
			Variables: []VariableMeta{{Name: "Repo"}}}}}, nil, [][]want{{
			{"pattern", CodeUnknownVariableMeta, 9, 9, `A description was sent for a variable named "Repo"`}}}},
		{"description on a route without variables", Draft{Routes: []DraftRoute{{Pattern: "gh", Destination: ok,
			Variables: []VariableMeta{{Name: ""}}}}}, nil, [][]want{{
			{"pattern", CodeUnknownVariableMeta, 2, 2, `A description was sent for a variable named ""`}}}},
		{"two descriptions for one variable", Draft{Routes: []DraftRoute{{Pattern: "gh/{repo}", Destination: "https://x.example/{repo}",
			Variables: []VariableMeta{{Name: "repo", Description: "a"}, {Name: "repo", Description: "b"}}}}}, nil, [][]want{{
			{"pattern", CodeDuplicateVariableMeta, 9, 9, "Two descriptions were sent for the variable repo."}}}},

		// A stored route is kept once.
		{"two routes claim one stored route", Draft{Routes: []DraftRoute{
			{ID: 5, Pattern: "gh", Destination: ok}, {Pattern: "gh/a", Destination: ok}, {ID: 5, Pattern: "gh/b", Destination: ok}}},
			[]want{{"link", CodeDuplicateRouteID, 0, 0, "Routes 1 and 3 both replace the same saved route."}},
			[][]want{none, none, none}},
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
			if !strings.HasSuffix(p.Message, ".") {
				t.Errorf("%s: message %q is not a sentence", where, p.Message)
			}
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link, problems := ValidateDraft(tt.draft)
			if tt.link == nil && tt.routes == nil {
				if problems.HasErrors() || problems.Count() != 0 || link.Name == "" {
					t.Fatalf("ValidateDraft = %+v, %+v; want a link and no problems", link, problems)
				}
				return
			}
			if link.Name != "" || link.Routes != nil || link.Description != "" {
				t.Errorf("ValidateDraft returned link %+v with problems", link)
			}
			check(t, "link", problems.Link, tt.link)
			if len(problems.Routes) != len(tt.draft.Routes) || len(problems.Tokens) != len(tt.draft.Routes) {
				t.Fatalf("problems has %d route entries and %d token entries for a draft of %d routes",
					len(problems.Routes), len(problems.Tokens), len(tt.draft.Routes))
			}
			total := len(tt.link)
			for i := range tt.draft.Routes {
				check(t, fmt.Sprintf("route %d", i+1), problems.Routes[i], tt.routes[i])
				total += len(tt.routes[i])
			}
			if problems.Count() != total || !problems.HasErrors() {
				t.Errorf("Count() = %d, HasErrors() = %v; want %d problems", problems.Count(), problems.HasErrors(), total)
			}
			first, ok := problems.First()
			if !ok || first.Message == "" {
				t.Errorf("First() = %+v, %v; want the first problem", first, ok)
			}
		})
	}
}

// Warnings do not block saving (DIR-2): the link comes back, ready to save,
// together with them.
func TestValidateDraftWarnings(t *testing.T) {
	type want struct {
		field, code string
		start, end  int
		mentions    string
	}
	tests := []struct {
		name  string
		draft Draft
		want  [][]want
	}{
		{"unused variable", draft("reports/{month}/{day}", "https://example.com/?m={month}"), [][]want{{
			{"destination", CodeDestinationUnusedVariable, 30, 30, "doesn't use {day}"}}}},
		{"unused in one route of two", draft("gh", "https://example.com", "gh/{repo}", "https://example.com/all"), [][]want{nil, {
			{"destination", CodeDestinationUnusedVariable, 23, 23, "doesn't use {repo}"}}}},
		{"example its variable refuses", Draft{Routes: []DraftRoute{{Pattern: "reports/{month:int}", Destination: "https://example.com/?m={month}",
			Variables: []VariableMeta{{Name: "month", Example: "October"}}}}}, [][]want{{
			{"pattern", CodeExampleRejected, 8, 19, `The example "October" for month is not a value it accepts. It accepts a whole number.`}}}},
		{"example that would climb the path", Draft{Routes: []DraftRoute{{Pattern: "docs/{page}", Destination: "https://example.com/docs/{page}",
			Variables: []VariableMeta{{Name: "page", Example: ".."}}}}}, [][]want{{
			{"pattern", CodeExampleRejected, 5, 11, `The example ".." for page`}}}},
		// Within the pattern's problems the order is by position, whatever
		// rule found them.
		{"example warning and unknown description", Draft{Routes: []DraftRoute{{Pattern: "x/{a:int}", Destination: "https://example.com/{a}",
			Variables: []VariableMeta{{Name: "a", Example: "x"}}}}}, [][]want{{
			{"pattern", CodeExampleRejected, 2, 9, `The example "x" for a`}}}},
		{"both kinds, pattern first", Draft{Routes: []DraftRoute{{Pattern: "x/{a:int}/{b}", Destination: "https://example.com/{a}",
			Variables: []VariableMeta{{Name: "a", Example: "x"}}}}}, [][]want{{
			{"pattern", CodeExampleRejected, 2, 9, `The example "x" for a`},
			{"destination", CodeDestinationUnusedVariable, 23, 23, "doesn't use {b}"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link, problems := ValidateDraft(tt.draft)
			if problems.HasErrors() {
				t.Fatalf("ValidateDraft reported errors: %+v", problems)
			}
			if _, any := problems.First(); any {
				t.Error("First() returned a warning; it is for errors")
			}
			if link.Name == "" || len(link.Routes) != len(tt.draft.Routes) {
				t.Fatalf("ValidateDraft returned link %+v; a draft with only warnings can be saved", link)
			}
			total := 0
			for i, wants := range tt.want {
				got := problems.Routes[i]
				if len(got) != len(wants) {
					t.Fatalf("route %d: problems %+v, want %d", i+1, got, len(wants))
				}
				for j, w := range wants {
					p := got[j]
					if p.Severity != SeverityWarning || p.Field != w.field || p.Code != w.code || p.Start != w.start ||
						p.End != w.end || !strings.Contains(p.Message, w.mentions) {
						t.Errorf("route %d: problem %d = %+v\nwant %+v", i+1, j, p, w)
					}
				}
				total += len(wants)
			}
			if problems.Count() != total {
				t.Errorf("Count() = %d, want %d", problems.Count(), total)
			}
		})
	}
}

// An accepted example is no warning, including where "accepted" depends on
// the destination.
func TestValidateDraftAcceptedExamples(t *testing.T) {
	d := Draft{Routes: []DraftRoute{
		{Pattern: "x/{a:int}/{b}/{c:regex([A-Z]+)}", Destination: "https://example.com/{a}?b={b}&c={c}",
			Variables: []VariableMeta{{Name: "a", Example: "-7"}, {Name: "b", Example: ".."}, {Name: "c", Example: "OPS"}}},
	}}
	if _, problems := ValidateDraft(d); problems.Count() != 0 {
		t.Errorf("problems = %+v, want none", problems)
	}
}

func TestValidateDraftRouteLimit(t *testing.T) {
	build := func(n int) Draft {
		var d Draft
		for i := range n {
			d.Routes = append(d.Routes, DraftRoute{Pattern: fmt.Sprintf("x/r%d", i), Destination: "https://example.com"})
		}
		return d
	}
	link, problems := ValidateDraft(build(MaxRoutes))
	if problems.Count() != 0 || len(link.Routes) != MaxRoutes {
		t.Fatalf("%d routes: problems %+v", MaxRoutes, problems)
	}
	link, problems = ValidateDraft(build(MaxRoutes + 1))
	if link.Name != "" || len(problems.Link) != 1 || problems.Link[0].Code != CodeTooManyRoutes ||
		problems.Link[0].Message != "A link can have at most 20 routes; this one has 21." {
		t.Fatalf("%d routes: link %q, problems %+v; want %s", MaxRoutes+1, link.Name, problems.Link, CodeTooManyRoutes)
	}
	if problems.Link[0].Field != FieldLink {
		t.Errorf("field = %q, want %q: the problem is about the link, not a pattern", problems.Link[0].Field, FieldLink)
	}
	// The routes of such a draft are not read at all: see
	// TestValidateDraftDoesNotReadTooManyRoutes.
	if len(problems.Routes) != 0 || len(problems.Tokens) != 0 || problems.Count() != 1 {
		t.Errorf("problems = %+v, want the one link-level problem and nothing per route", problems)
	}
}

// A request body has room for thousands of routes. A draft with more than a
// link can have is refused for that alone, in no time: reading every one of
// its patterns, regular expressions included, took seconds and yielded tens
// of thousands of problems.
func TestValidateDraftDoesNotReadTooManyRoutes(t *testing.T) {
	var d Draft
	for i := range 5000 {
		// Six regular expressions each, and every route wrong in its own way.
		d.Routes = append(d.Routes, DraftRoute{
			ID:          7,
			Pattern:     fmt.Sprintf("r%d/{a:regex([a-z]{0,300})}/{b:regex([a-z]{0,300})}/{c:regex([a-z]{0,300})}/{d:regex([a-z]{0,300})}/{e:regex([a-z]{0,300})}/{f:regex([a-z]{0,300})}", i),
			Destination: "not a url",
		})
	}
	start := time.Now()
	link, problems := ValidateDraft(d)
	took := time.Since(start)

	if link.Name != "" || problems.Count() != 1 || len(problems.Link) != 1 || problems.Link[0].Code != CodeTooManyRoutes {
		t.Fatalf("ValidateDraft = %q, %d problems (link: %+v); want only %s", link.Name, problems.Count(), problems.Link, CodeTooManyRoutes)
	}
	if got, want := problems.Link[0].Message, "A link can have at most 20 routes; this one has 5000."; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if len(problems.Routes) != 0 || len(problems.Tokens) != 0 {
		t.Errorf("the routes were read: %d route entries, %d token entries", len(problems.Routes), len(problems.Tokens))
	}
	// Reading them takes seconds; not reading them takes microseconds. The
	// bound is loose so that a busy machine cannot fail it.
	if took > 200*time.Millisecond {
		t.Errorf("ValidateDraft took %v for a draft that is refused at once", took)
	}
}

// Every regular expression has its own size limit, and the ones of a link
// have a limit together: a request can make all of them run (NFR-8).
func TestValidateDraftRegexBudget(t *testing.T) {
	// Each expression is under MaxRegexProgram on its own.
	routes := func(n int) Draft {
		var d Draft
		for i := range n {
			d.Routes = append(d.Routes, DraftRoute{
				Pattern:     fmt.Sprintf("slow/{v:regex((?:a?){%d}b)}", 470+i),
				Destination: "https://example.com/?v={v}",
			})
		}
		return d
	}
	size := func(n int) (total int) {
		for _, r := range routes(n).Routes {
			p, problems := ParsePattern(r.Pattern)
			if len(problems) != 0 {
				t.Fatalf("%s: %+v", r.Pattern, problems)
			}
			total += regexProgram(p)
		}
		return total
	}
	if got := size(4); got > MaxLinkRegexProgram || got < MaxLinkRegexProgram*9/10 {
		t.Fatalf("four routes have %d instructions; the test wants them just under %d", got, MaxLinkRegexProgram)
	}
	if link, problems := ValidateDraft(routes(4)); problems.Count() != 0 || len(link.Routes) != 4 {
		t.Fatalf("four routes within the budget: %+v", problems)
	}
	link, problems := ValidateDraft(routes(5))
	if link.Name != "" || problems.Count() != 1 || len(problems.Link) != 1 {
		t.Fatalf("five routes over the budget: link %q, problems %+v; want one link-level problem", link.Name, problems)
	}
	got := problems.Link[0]
	if got.Code != CodePatternRegexTooComplex || got.Field != FieldLink || got.Severity != SeverityError ||
		!strings.Contains(got.Message, "taken together") {
		t.Errorf("problem = %+v, want %s on the link", got, CodePatternRegexTooComplex)
	}
	// A link full of ordinary expressions is nowhere near the budget.
	if total := regexProgramOfDraft(t, benchRoutes()); total > MaxLinkRegexProgram/4 {
		t.Errorf("the benchmark link, eight ordinary expressions, uses %d of %d", total, MaxLinkRegexProgram)
	}
}

func regexProgramOfDraft(t *testing.T, routes []DraftRoute) (total int) {
	t.Helper()
	for _, r := range routes {
		p, problems := ParsePattern(r.Pattern)
		if len(problems) != 0 {
			t.Fatalf("%s: %+v", r.Pattern, problems)
		}
		total += regexProgram(p)
	}
	return total
}

// The Directory's own hosts reach the destination rule through the options.
func TestValidateDraftOwnHosts(t *testing.T) {
	d := draft("self", "http://localhost:4655/-/links", "self/other", "http://localhost:3000/")
	if _, problems := ValidateDraft(d); problems.Count() != 0 {
		t.Fatalf("without options: problems = %+v, want none", problems)
	}
	link, problems := ValidateDraft(d, ValidateOptions{OwnHosts: []string{"localhost:4655", "127.0.0.1:4655"}})
	if link.Name != "" || len(problems.Routes[0]) != 1 || problems.Routes[0][0].Code != CodeDestinationDirectory || len(problems.Routes[1]) != 0 {
		t.Errorf("with options: link %q, problems %+v; want %s on the first route only", link.Name, problems, CodeDestinationDirectory)
	}
}

// The validation endpoint returns the tokens of every route, valid or not.
func TestValidateDraftTokens(t *testing.T) {
	d := draft(
		"reports/{month:int}/{day", "https://reports.example.com/daily?month={month}&day={day}", // plan 3.5
		"reports", "https://reports.example.com/",
		"", "",
	)
	_, problems := ValidateDraft(d)
	want := [][]Token{
		{{Kind: TokenLiteral, Start: 0, End: 7}, {Kind: TokenVariable, Start: 8, End: 19, Name: "month", Type: "int"}},
		{{Kind: TokenLiteral, Start: 0, End: 7}},
		nil,
	}
	if !reflect.DeepEqual(problems.Tokens, want) {
		t.Errorf("Tokens =\n  %+v\nwant\n  %+v", problems.Tokens, want)
	}
	if p := problems.Routes[0]; len(p) != 1 || p[0].Code != CodePatternUnclosedVariable || p[0].Start != 20 || p[0].End != 24 ||
		p[0].Message != "This variable is missing its closing }." {
		t.Errorf("route 1 problems = %+v, want the plan's example", p)
	}
}

func TestDraftProblemsFirst(t *testing.T) {
	p := func(msg string) Problem { return Problem{Severity: SeverityError, Message: msg} }
	w := func(msg string) Problem { return Problem{Severity: SeverityWarning, Message: msg} }
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
		{"warnings are passed over", DraftProblems{Link: []Problem{w("a")}, Routes: [][]Problem{{w("b")}, {w("c"), p("error")}}}, "error", 4},
		{"only warnings", DraftProblems{Link: []Problem{w("a")}, Routes: [][]Problem{{w("b")}}}, "", 2},
	}
	for _, tt := range tests {
		got, ok := tt.problems.First()
		if got.Message != tt.want || ok != (tt.want != "") {
			t.Errorf("%s: First() = %q, %v; want %q", tt.name, got.Message, ok, tt.want)
		}
		if tt.problems.Count() != tt.count || tt.problems.HasErrors() != (tt.want != "") {
			t.Errorf("%s: Count() = %d, HasErrors() = %v; want %d, %v",
				tt.name, tt.problems.Count(), tt.problems.HasErrors(), tt.count, tt.want != "")
		}
	}
}

// Within one route the pattern's problems are in reading order whichever
// rule found them, and they all come before the destination's.
func TestValidateDraftProblemOrder(t *testing.T) {
	_, problems := ValidateDraft(Draft{Routes: []DraftRoute{{
		Pattern: "x/{a:int}/{b}", Destination: "https://example.com/{a}/{c}",
		Variables: []VariableMeta{{Name: "zzz"}, {Name: "a", Example: "x"}},
	}}})
	var got []string
	for _, p := range problems.Routes[0] {
		got = append(got, fmt.Sprintf("%s %s %d", p.Severity, p.Code, p.Start))
	}
	want := []string{
		"warning pattern.example_rejected 2",
		"error pattern.unknown_variable_meta 13",
		"error destination.undeclared_variable 24",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
	if first, ok := problems.First(); !ok || first.Code != CodeUnknownVariableMeta {
		t.Errorf("First() = %+v, want the first error", first)
	}
}
