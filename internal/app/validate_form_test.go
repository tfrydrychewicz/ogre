package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// The variables table of the form reads DraftVariables: the variables that
// can be read so far, where each one is, and a regex's expression on its own.
func TestDraftVariables(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []VariableCheck
	}{
		{"none", "roadmap", []VariableCheck{}},
		{"empty", "", []VariableCheck{}},
		{"typed", "reports/{month:int}/{day:number}", []VariableCheck{
			{Name: "month", Type: "int", Accepts: "a whole number", Start: 8, End: 19},
			{Name: "day", Type: "number", Accepts: "a number such as 42 or 3.5", Start: 20, End: 32},
		}},
		{"string written out and in capitals", "a/{x:STRING}/{y}", []VariableCheck{
			{Name: "x", Type: "string", Accepts: "any text", Start: 2, End: 12},
			{Name: "y", Type: "string", Accepts: "any text", Start: 13, End: 16},
		}},
		{"regex with parentheses, slashes and braces", "jira/{key:regex(^([A-Z]+)/{1}-[0-9]+$)}", []VariableCheck{
			{Name: "key", Type: "regex", Accepts: "text matching ^([A-Z]+)/{1}-[0-9]+$",
				Expression: "^([A-Z]+)/{1}-[0-9]+$", Start: 5, End: 39},
		}},
		{"unfinished: only what can be read", "reports/{month:int}/{day", []VariableCheck{
			{Name: "month", Type: "int", Accepts: "a whole number", Start: 8, End: 19},
		}},
		{"a broken regex is not listed", "a/{x:regex(a**)}/{y:int}", []VariableCheck{
			{Name: "y", Type: "int", Accepts: "a whole number", Start: 17, End: 24},
		}},
		{"a name used twice is listed once", "a/{x}/{x:int}", []VariableCheck{
			{Name: "x", Type: "string", Accepts: "any text", Start: 2, End: 5},
		}},
		{"non-ASCII expression: byte offsets", "a/{x:regex(ż+)}/{y}", []VariableCheck{
			{Name: "x", Type: "regex", Accepts: "text matching ż+", Expression: "ż+", Start: 2, End: 16},
			{Name: "y", Type: "string", Accepts: "any text", Start: 17, End: 20},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DraftVariables(tt.pattern)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DraftVariables(%q) =\n %+v\nwant\n %+v", tt.pattern, got, tt.want)
			}
			for _, v := range got {
				span := tt.pattern[v.Start:v.End]
				if !strings.HasPrefix(span, "{"+v.Name) || !strings.HasSuffix(span, "}") {
					t.Errorf("span of %s is %q, want the variable's whole text", v.Name, span)
				}
			}
		})
	}
}

// What the form does with a type chosen in the table, put back into the
// pattern, is read by the link model as that type: the span and the
// expression are enough to rewrite a variable without parsing.
func TestDraftVariablesRewrite(t *testing.T) {
	pattern := "jira/{key:regex(^[A-Z]+-[0-9]+$)}/{n}"
	vars := DraftVariables(pattern)
	key := vars[0]
	rewritten := pattern[:key.Start] + "{" + key.Name + ":regex(" + key.Expression + ")}" + pattern[key.End:]
	if rewritten != pattern {
		t.Errorf("rewriting the regex unchanged gave %q, want %q", rewritten, pattern)
	}
	n := vars[1]
	rewritten = pattern[:n.Start] + "{" + n.Name + ":int}" + pattern[n.End:]
	if _, problems := linkmodel.ParsePattern(rewritten); len(problems) != 0 {
		t.Fatalf("%q: %+v", rewritten, problems)
	}
	if got := DraftVariables(rewritten)[1]; got.Type != linkmodel.TypeInt {
		t.Errorf("after the rewrite n is %q, want int", got.Type)
	}
}

// The live preview decides with the link model, as a saved link would: the
// same specificity, encoding and reasons.
func TestPreviewDraft(t *testing.T) {
	reports := DraftRoute{
		Pattern:     "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
		Destination: "https://reports.example.com/daily?month={month}&day={day}",
		Variables:   []linkmodel.VariableMeta{{Name: "month", Description: "Month, 1 to 12", Example: "10"}},
	}
	gh := draft(
		"gh/{repo}", "https://github.com/acme/{repo}",
		"gh/new", "https://github.com/organizations/acme/repositories/new",
	)
	search := draft("search/{q}", "https://search.example.com/?q={q}")
	broken := draft(
		"gh/{repo}", "https://github.com/acme/{repo}",
		"gh/{name}", "https://github.com/acme/{name}", // same shape: an error on route 1
	)
	tests := []struct {
		name  string
		draft Draft
		req   PreviewRequest
		want  Preview
	}{
		{"hit", Draft{Routes: []DraftRoute{reports}}, PreviewRequest{Route: 0, Path: "reports/10/13"},
			Preview{Kind: PreviewHit, Route: 0, Destination: "https://reports.example.com/daily?month=10&day=13",
				Values: map[string]string{"month": "10", "day": "13"}}},
		{"name ignores case", Draft{Routes: []DraftRoute{reports}}, PreviewRequest{Route: 0, Path: "Reports/10/13/"},
			Preview{Kind: PreviewHit, Route: 0, Destination: "https://reports.example.com/daily?month=10&day=13",
				Values: map[string]string{"month": "10", "day": "13"}}},
		{"value rejected, explained with the description", Draft{Routes: []DraftRoute{reports}},
			PreviewRequest{Route: 0, Path: "reports/13/01"},
			Preview{Kind: PreviewMissKnown, Route: -1, Reasons: []PreviewReason{{Route: 0, Reason: linkmodel.Reason{
				Pattern: reports.Pattern, Code: linkmodel.ReasonValueRejected, Variable: "month", Value: "13",
				Accepts: "text matching ^(0?[1-9]|1[0-2])$",
				Message: `month (Month, 1 to 12) doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$. For example: "10".`,
			}}}}},
		{"a literal beats a variable, whichever route asked", gh, PreviewRequest{Route: 0, Path: "gh/new"},
			Preview{Kind: PreviewHit, Route: 1, Destination: "https://github.com/organizations/acme/repositories/new"}},
		{"a miss explains only the route asked about", gh, PreviewRequest{Route: 1, Path: "gh/a/b"},
			Preview{Kind: PreviewMissKnown, Route: -1, Reasons: []PreviewReason{{Route: 1, Reason: linkmodel.Reason{
				Pattern: "gh/new", Code: linkmodel.ReasonSegmentCount,
				Message: missMessage(t, gh, 1, "gh/a/b"),
			}}}}},
		{"a miss explains every route when none was asked about", gh, PreviewRequest{Route: -1, Path: "gh"},
			Preview{Kind: PreviewMissKnown, Route: -1, Reasons: []PreviewReason{
				{Route: 0, Reason: linkmodel.Reason{Pattern: "gh/{repo}", Code: linkmodel.ReasonSegmentCount, Message: missMessage(t, gh, 0, "gh")}},
				{Route: 1, Reason: linkmodel.Reason{Pattern: "gh/new", Code: linkmodel.ReasonSegmentCount, Message: missMessage(t, gh, 1, "gh")}},
			}}},
		{"values are decoded once and encoded again", search, PreviewRequest{Route: 0, Path: "search/a%2Fb%20%26c"},
			Preview{Kind: PreviewHit, Route: 0, Destination: "https://search.example.com/?q=a%2Fb%20%26c",
				Values: map[string]string{"q": "a/b &c"}}},
		{"the route asked about has errors", broken, PreviewRequest{Route: 1, Path: "gh/x"},
			Preview{Kind: PreviewInvalid, Route: -1, Message: "Fix the problems of this route to see where it leads."}},
		{"routes with errors take no part", broken, PreviewRequest{Route: -1, Path: "gh/x"},
			Preview{Kind: PreviewHit, Route: 0, Destination: "https://github.com/acme/x", Values: map[string]string{"repo": "x"}}},
		{"nothing can be saved", draft("gh/{repo", "https://github.com/acme/{repo}"), PreviewRequest{Route: -1, Path: "gh/x"},
			Preview{Kind: PreviewInvalid, Route: -1, Message: "Fix the problems above to see where this link leads."}},
		{"route out of range", search, PreviewRequest{Route: 3, Path: "search/x"},
			Preview{Kind: PreviewInvalid, Route: -1, Message: "This route does not exist."}},
		{"malformed escape", search, PreviewRequest{Route: 0, Path: "search/a%zz"},
			Preview{Kind: PreviewBadRequest, Route: -1, Message: "This go link can't be read: a % in it is not followed by two hexadecimal digits, or it is not valid text."}},
		{"empty path", search, PreviewRequest{Route: 0, Path: "/"},
			Preview{Kind: PreviewBadRequest, Route: -1, Message: "There is no go link to try yet."}},
		{"a value that would climb out of the path", draft("docs/{page}", "https://docs.example.com/{page}"),
			PreviewRequest{Route: 0, Path: "docs/.."},
			Preview{Kind: PreviewMissKnown, Route: -1, Reasons: []PreviewReason{{Route: 0, Reason: linkmodel.Reason{
				Pattern: "docs/{page}", Code: linkmodel.ReasonValueNotAllowed, Variable: "page", Value: "..", Accepts: "any text",
				Message: `page can't be "..", because its value becomes part of the destination's path.`,
			}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, st := newApp(t)
			check := a.Validate(context.Background(), localActor, ValidateRequest{Draft: tt.draft})
			got := PreviewDraft(tt.draft, check, tt.req)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PreviewDraft(%q) =\n %+v\nwant\n %+v", tt.req.Path, got, tt.want)
			}
			if n := st.calls.Load(); n != 0 {
				t.Errorf("validating and previewing made %d store calls, want none", n)
			}
		})
	}
}

// missMessage is the sentence the link model gives for route i of d missing
// path: the preview passes it on, and the test should not restate wording
// that lives in linkmodel.
func missMessage(t *testing.T, d Draft, i int, path string) string {
	t.Helper()
	link := linkmodel.Link{Name: "gh"}
	for j, r := range d.Routes {
		link.Routes = append(link.Routes, linkmodel.Route{Position: j, Pattern: r.Pattern, Destination: r.Destination})
	}
	segments, err := linkmodel.SplitPath(path)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, reasons, _ := linkmodel.Compile(link).Match(segments)
	if ok || len(reasons) <= i {
		t.Fatalf("%q matched d, or gave %d reasons", path, len(reasons))
	}
	return reasons[i].Message
}

// The preview of an edit is the draft's, not the saved link's: a save
// replaces every route, so that is what the user will get.
func TestPreviewDraftOfAnEdit(t *testing.T) {
	a, _ := newApp(t)
	saved := mustCreate(t, a, ghDraft())
	edit := editOf(saved)
	edit.Routes[2].Destination = "https://code.example.com/{repo}"
	edit.Routes = edit.Routes[1:] // the bare "gh" route is being removed

	check := a.Validate(context.Background(), localActor, ValidateRequest{LinkID: saved.ID, Draft: edit})
	if !check.Valid {
		t.Fatalf("the edit is not valid: %+v", check)
	}
	if got := PreviewDraft(edit, check, PreviewRequest{Route: 1, Path: "gh/ogre"}); got.Kind != PreviewHit || got.Destination != "https://code.example.com/ogre" || got.Route != 1 {
		t.Errorf("gh/ogre previews as %+v, want a hit on the edited destination", got)
	}
	if got := PreviewDraft(edit, check, PreviewRequest{Route: -1, Path: "gh"}); got.Kind != PreviewMissKnown {
		t.Errorf("gh previews as %+v, want a miss: the draft has no bare route any more", got)
	}
	if d := a.Resolve("gh"); d.Kind != DecisionHit {
		t.Errorf("the saved link changed: gh resolves to %v", d.Kind)
	}
}
