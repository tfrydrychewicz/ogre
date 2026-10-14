package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

func TestValidateTokensAndSamples(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	existing := mustCreate(t, a, ghDraft())
	before := dump(t, st)
	st.calls.Store(0)

	req := ValidateRequest{
		Draft: Draft{Description: "Daily reports", Routes: []DraftRoute{
			{Pattern: "Reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
				Destination: "https://reports.example.com/daily?month={month}&day={day}"},
			{Pattern: "reports/by/{team}", Destination: "https://reports.example.com/teams/{team}"},
			{Pattern: "reports/{ratio:number}", Destination: "https://reports.example.com/?r={ratio}"},
		}},
		Samples: []map[string][]string{
			{"month": {"10", "13", "07", "7", ""}, "day": {"13", "x", "-4", "1.5"}, "year": {"2026"}},
			{"team": {"platform", "..", ".", "a/b", "..."}},
			// Fewer entries than routes: the third route has no samples.
		},
	}
	res := a.Validate(ctx, localActor, req)

	if !res.Valid || len(res.Problems) != 0 {
		t.Errorf("Valid = %v with link-level problems %+v, want a valid draft", res.Valid, res.Problems)
	}
	if want := (NameCheck{Value: "reports", Available: true}); !reflect.DeepEqual(res.Name, want) {
		t.Errorf("Name = %+v, want %+v", res.Name, want)
	}
	if len(res.Routes) != 3 {
		t.Fatalf("Routes has %d entries, want 3", len(res.Routes))
	}

	// Tokens: every segment of the pattern as written, with offsets into
	// that text, so a client highlights without parsing.
	p0 := req.Draft.Routes[0].Pattern
	wantTokens := [][]linkmodel.Token{
		{
			{Kind: linkmodel.TokenLiteral, Start: 0, End: 7},
			{Kind: linkmodel.TokenVariable, Start: 8, End: 41, Name: "month", Type: linkmodel.TypeRegex},
			{Kind: linkmodel.TokenVariable, Start: 42, End: len(p0), Name: "day", Type: linkmodel.TypeInt},
		},
		{
			{Kind: linkmodel.TokenLiteral, Start: 0, End: 7},
			{Kind: linkmodel.TokenLiteral, Start: 8, End: 10},
			{Kind: linkmodel.TokenVariable, Start: 11, End: 17, Name: "team", Type: linkmodel.TypeString},
		},
		{
			{Kind: linkmodel.TokenLiteral, Start: 0, End: 7},
			{Kind: linkmodel.TokenVariable, Start: 8, End: 22, Name: "ratio", Type: linkmodel.TypeNumber},
		},
	}
	for i, want := range wantTokens {
		if !reflect.DeepEqual(res.Routes[i].Tokens, want) {
			t.Errorf("route %d tokens = %+v\n            want %+v", i, res.Routes[i].Tokens, want)
		}
		if len(res.Routes[i].Problems) != 0 {
			t.Errorf("route %d problems = %+v, want none", i, res.Routes[i].Problems)
		}
	}
	if got := p0[8:41]; got != "{month:regex(^(0?[1-9]|1[0-2])$)}" {
		t.Errorf("the month token covers %q", got)
	}

	// Samples: what the route would accept, by the parsed types and the
	// dot rule for a variable used in the destination's path (NFR-10).
	wantSamples := []map[string]map[string]bool{
		{
			"month": {"10": true, "13": false, "07": true, "7": true, "": false},
			"day":   {"13": true, "x": false, "-4": true, "1.5": false},
			// "year" is not a variable of the pattern: left out.
		},
		{"team": {"platform": true, "..": false, ".": false, "a/b": true, "...": true}},
		nil,
	}
	for i, want := range wantSamples {
		if !reflect.DeepEqual(res.Routes[i].Samples, want) {
			t.Errorf("route %d samples = %v, want %v", i, res.Routes[i].Samples, want)
		}
	}
	// Each refused sample has the sentence a miss page would show for it,
	// so a launcher can say it beside the field (VAR-5); accepted ones have
	// none.
	wantReasons := []map[string]map[string]string{
		{
			"month": {
				"13": `month doesn't accept "13". It accepts text matching ^(0?[1-9]|1[0-2])$.`,
				"":   `month doesn't accept "". It accepts text matching ^(0?[1-9]|1[0-2])$.`,
			},
			"day": {
				"x":   `day doesn't accept "x". It accepts a whole number.`,
				"1.5": `day doesn't accept "1.5". It accepts a whole number.`,
			},
		},
		{"team": {
			"..": `team can't be "..", because its value becomes part of the destination's path.`,
			".":  `team can't be ".", because its value becomes part of the destination's path.`,
		}},
		nil,
	}
	for i, want := range wantReasons {
		if !reflect.DeepEqual(res.Routes[i].SampleReasons, want) {
			t.Errorf("route %d sample reasons = %q, want %q", i, res.Routes[i].SampleReasons, want)
		}
	}

	// Validate is a pure check: no store call, nothing stored, no revision,
	// nothing new resolves.
	if got := st.calls.Load(); got != 0 {
		t.Errorf("Validate made %d store calls, want 0", got)
	}
	if after := dump(t, st); after != before {
		t.Errorf("Validate changed the store:\nbefore: %s\nafter:  %s", before, after)
	}
	if revs := st.revisions(t, existing.ID); len(revs) != 1 {
		t.Errorf("the existing link has %d revisions after Validate, want 1", len(revs))
	}
	if n := a.index.Load().Len(); n != 1 {
		t.Errorf("index holds %d links after Validate, want 1", n)
	}
	misses(t, a, "/reports/10/13", DecisionMissUnknown)

	// And what it promised holds: the draft saves, and the samples resolve
	// or miss as it said.
	if _, err := a.CreateLink(ctx, localActor, req.Draft); err != nil {
		t.Fatalf("CreateLink of a draft Validate called valid: %v", err)
	}
	for month, ok := range wantSamples[0]["month"] {
		if month == "" {
			continue // an empty segment is no segment
		}
		if hit := a.Resolve("/reports/"+month+"/1").Kind == DecisionHit; hit != ok {
			t.Errorf("month %q: Validate said accepted=%v, Resolve hit=%v", month, ok, hit)
		}
	}
	for _, team := range []string{"platform", "..", ".", "..."} {
		if hit := a.Resolve("/reports/by/"+team).Kind == DecisionHit; hit != wantSamples[1]["team"][team] {
			t.Errorf("team %q: Validate said accepted=%v, Resolve hit=%v", team, wantSamples[1]["team"][team], hit)
		}
	}
}

// Samples are answered from the pattern even while the destination is still
// wrong, and while the pattern is, for the variables that can be read.
func TestValidateSamplesOnAnUnfinishedRoute(t *testing.T) {
	a, _ := newApp(t)
	samples := []map[string][]string{{"n": {"7", "x", ".."}}}

	res := a.Validate(context.Background(), localActor, ValidateRequest{
		Draft: draft("count/{n:int}", "ftp://nope/{n}"), Samples: samples})
	if res.Valid || !reflect.DeepEqual(res.Routes[0].Samples, map[string]map[string]bool{"n": {"7": true, "x": false, "..": false}}) {
		t.Errorf("with a bad destination: valid %v, samples %v", res.Valid, res.Routes[0].Samples)
	}
	// A text variable whose destination is unreadable: only the type is
	// asked, and text accepts "..".
	res = a.Validate(context.Background(), localActor, ValidateRequest{
		Draft: draft("count/{n}", ""), Samples: samples})
	if !reflect.DeepEqual(res.Routes[0].Samples, map[string]map[string]bool{"n": {"7": true, "x": true, "..": true}}) {
		t.Errorf("with no destination: samples %v", res.Routes[0].Samples)
	}
	res = a.Validate(context.Background(), localActor, ValidateRequest{
		Draft: draft("count/{n:int", "https://example.com/{n}"), Samples: samples})
	if res.Valid || len(res.Routes[0].Samples) != 0 {
		t.Errorf("with a variable that cannot be read: valid %v, samples %v; want no sample results", res.Valid, res.Routes[0].Samples)
	}
	// The example of plan section 3.5: the pattern is not finished, and the
	// form already shows the name, and what the finished variable accepts.
	res = a.Validate(context.Background(), localActor, ValidateRequest{
		Draft:   draft("reports/{month:int}/{day", "https://reports.example.com/daily?month={month}&day={day}"),
		Samples: []map[string][]string{{"month": {"10", "x"}, "day": {"13"}}}})
	if res.Valid || res.Name.Value != "reports" || !res.Name.Available ||
		!reflect.DeepEqual(res.Routes[0].Samples, map[string]map[string]bool{"month": {"10": true, "x": false}}) {
		t.Errorf("with an unfinished pattern: valid %v, name %+v, samples %v", res.Valid, res.Name, res.Routes[0].Samples)
	}
	if got := codesOf(res.Routes[0].Problems); !reflect.DeepEqual(got, []string{linkmodel.CodePatternUnclosedVariable}) {
		t.Errorf("with an unfinished pattern: problems %v, want the pattern's alone", got)
	}
	// More sample entries than routes are ignored.
	res = a.Validate(context.Background(), localActor, ValidateRequest{
		Draft: draft("count/{n:int}", "https://example.com/{n}"), Samples: append(samples, samples...)})
	if !res.Valid || len(res.Routes) != 1 {
		t.Errorf("with surplus samples: %+v", res)
	}
}

func TestValidateName(t *testing.T) {
	ctx := context.Background()
	a, st := newApp(t)
	grace := st.addUser(t, "Grace")
	gh := mustCreate(t, a, ghDraft())
	wiki, err := a.CreateLink(ctx, grace, draft("wiki", "https://wiki.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	st.calls.Store(0)

	ok := "https://example.com"
	tests := []struct {
		name  string
		actor Actor
		req   ValidateRequest
		value string
		// Whose link holds the name; 0 when it is available.
		takenBy  int64
		editable bool
		valid    bool
	}{
		{"a free name", localActor, ValidateRequest{Draft: draft("Roadmap", ok)}, "roadmap", 0, false, true},
		{"a free name that differs by a separator", localActor, ValidateRequest{Draft: draft("g-h", ok)}, "g-h", 0, false, true},
		{"taken, ignoring case, by the actor's own link", localActor, ValidateRequest{Draft: draft("GH", ok)}, "gh", gh.ID, true, false},
		{"taken by someone else's link", grace, ValidateRequest{Draft: draft("gh/x", ok)}, "gh", gh.ID, false, false},
		{"taken by someone else's link, asked by an admin", localActor, ValidateRequest{Draft: draft("wiki", ok)}, "wiki", wiki.ID, true, false},
		{"taken, asked by nobody", Actor{}, ValidateRequest{Draft: draft("wiki", ok)}, "wiki", wiki.ID, false, false},

		// Editing: a link's own name is available to it.
		{"editing a link under its own name", localActor, ValidateRequest{LinkID: gh.ID, Draft: editOf(gh)}, "gh", 0, false, true},
		{"renaming to a free name", localActor, ValidateRequest{LinkID: gh.ID, Draft: draft("github", ok)}, "github", 0, false, true},
		{"renaming onto another link", grace, ValidateRequest{LinkID: wiki.ID, Draft: draft("gh", ok)}, "gh", gh.ID, false, false},
		{"another link's ID does not make a name one's own", localActor, ValidateRequest{LinkID: wiki.ID, Draft: draft("gh", ok)}, "gh", gh.ID, true, false},

		// An invalid draft still has a name worth checking: that of its
		// first readable pattern.
		{"invalid destination, free name", localActor, ValidateRequest{Draft: draft("roadmap", "nope")}, "roadmap", 0, false, false},
		{"invalid destination, taken name", localActor, ValidateRequest{Draft: draft("gh", "nope")}, "gh", gh.ID, true, false},
		{"first pattern unreadable, second readable", localActor, ValidateRequest{Draft: draft("{x}", ok, "Wiki/x", ok)}, "wiki", wiki.ID, true, false},
		{"no readable pattern", localActor, ValidateRequest{Draft: draft("a b", ok, "", ok)}, "", 0, false, false},
		{"no routes", localActor, ValidateRequest{}, "", 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := a.Validate(ctx, tt.actor, tt.req)
			if res.Name.Value != tt.value {
				t.Errorf("Name.Value = %q, want %q", res.Name.Value, tt.value)
			}
			if wantAvailable := tt.takenBy == 0 && tt.value != ""; res.Name.Available != wantAvailable {
				t.Errorf("Name.Available = %v, want %v", res.Name.Available, wantAvailable)
			}
			switch {
			case tt.takenBy == 0:
				if res.Name.Existing != nil || res.Name.ExistingEditable {
					t.Errorf("Name = %+v, want no existing link", res.Name)
				}
			case res.Name.Existing == nil || res.Name.Existing.ID != tt.takenBy || res.Name.Existing.Name != tt.value || len(res.Name.Existing.Routes) == 0:
				t.Errorf("Name.Existing = %+v, want link %d", res.Name.Existing, tt.takenBy)
			case res.Name.ExistingEditable != tt.editable:
				t.Errorf("Name.ExistingEditable = %v, want %v", res.Name.ExistingEditable, tt.editable)
			}
			if res.Valid != tt.valid {
				t.Errorf("Valid = %v, want %v (%+v)", res.Valid, tt.valid, res)
			}
			if len(res.Routes) != len(tt.req.Draft.Routes) {
				t.Errorf("Routes has %d entries for %d routes", len(res.Routes), len(tt.req.Draft.Routes))
			}
			if res.Name.Existing != nil {
				// A copy: the caller may do with it what it likes.
				res.Name.Existing.Routes[0].Destination = "https://evil.example"
			}
		})
	}
	hits(t, a, "/gh", "https://github.com/acme")
	hits(t, a, "/wiki", "https://wiki.example.com")
	if got := st.calls.Load(); got != 0 {
		t.Errorf("Validate made %d store calls, want 0", got)
	}

	// A link that no longer exists cannot be edited.
	res := a.Validate(ctx, localActor, ValidateRequest{LinkID: 4242, Draft: draft("roadmap", ok)})
	if res.Valid || !reflect.DeepEqual(codesOf(res.Problems), []string{CodeLinkNotFound}) || !res.Name.Available {
		t.Errorf("Validate for an unknown link = %+v, want invalid with %s", res, CodeLinkNotFound)
	}
}

// NFR-22: checking and saving are the same rules. For every draft, Validate
// says "valid" exactly when CreateLink saves it, and reports the problems
// CreateLink's error carries.
func TestValidateAgreesWithSaving(t *testing.T) {
	ctx := context.Background()
	ok := "https://example.com"
	many := Draft{}
	for i := 0; i <= linkmodel.MaxRoutes; i++ {
		many.Routes = append(many.Routes, DraftRoute{Pattern: "many/" + string(rune('a'+i)), Destination: ok})
	}
	drafts := map[string]Draft{
		"plain":                 draft("roadmap", ok),
		"variables":             draft("gh/{repo}/pr/{n:int}", ok+"/{repo}/{n}"),
		"warning only":          draft("gh/{repo}", ok),
		"taken name":            draft("Taken", ok),
		"taken and invalid":     draft("taken", "javascript:alert(1)"),
		"no routes":             {},
		"too many routes":       many,
		"same shape":            draft("x/{a}", ok+"/{a}", "x/{b}", ok+"/{b}"),
		"duplicate":             draft("x", ok, "X", ok),
		"name mismatch":         draft("x", ok, "y", ok),
		"unknown type":          draft("x/{d:date}", ok+"/{d}"),
		"bad regex":             draft("x/{d:regex(()}", ok+"/{d}"),
		"placeholder in host":   draft("x/{t}", "https://{t}.example.com/"),
		"undeclared":            draft("x/{m}", ok+"/{y}"),
		"credentials":           draft("x", "https://user:pw@example.com/"),
		"go link":               draft("x", "http://go/other"),
		"reserved":              draft("favicon.ico", ok),
		"unknown variable meta": {Routes: []DraftRoute{{Pattern: "x/{a}", Destination: ok + "/{a}", Variables: []linkmodel.VariableMeta{{Name: "b"}}}}},
		"rejected example":      {Routes: []DraftRoute{{Pattern: "x/{a:int}", Destination: ok + "/{a}", Variables: []linkmodel.VariableMeta{{Name: "a", Example: "seven"}}}}},
		"route IDs on a create": {Routes: []DraftRoute{{ID: 77, Pattern: "x", Destination: ok}}},
	}
	for name, d := range drafts {
		t.Run(name, func(t *testing.T) {
			a, _ := newApp(t)
			mustCreate(t, a, draft("taken", ok))

			res := a.Validate(ctx, localActor, ValidateRequest{Draft: d})
			saved, err := a.CreateLink(ctx, localActor, d)
			if res.Valid != (err == nil) {
				t.Fatalf("Validate says valid=%v, CreateLink says %v", res.Valid, err)
			}
			var (
				invalid *ValidationError
				taken   *NameTakenError
			)
			switch {
			case err == nil:
				if saved.Name != res.Name.Value {
					t.Errorf("saved as %q, Validate named it %q", saved.Name, res.Name.Value)
				}
			case errors.As(err, &invalid):
				if !reflect.DeepEqual(res.Problems, invalid.Problems) {
					t.Errorf("link-level problems differ:\nValidate   %+v\nCreateLink %+v", res.Problems, invalid.Problems)
				}
				for i := range res.Routes {
					if !reflect.DeepEqual(res.Routes[i].Problems, invalid.Routes[i]) {
						t.Errorf("route %d problems differ:\nValidate   %+v\nCreateLink %+v", i, res.Routes[i].Problems, invalid.Routes[i])
					}
				}
			case errors.As(err, &taken):
				if res.Name.Available || res.Name.Existing == nil || !reflect.DeepEqual(*res.Name.Existing, taken.Existing) ||
					res.Name.ExistingEditable != taken.Editable {
					t.Errorf("Validate named %+v, CreateLink %+v", res.Name, taken)
				}
			default:
				t.Errorf("CreateLink failed with %v", err)
			}
		})
	}
}

// A draft with more routes than a link can have is refused as a whole. Its
// routes are not read, neither for problems nor for a name nor for samples: a
// request body has room for thousands of them, and each costs time.
func TestValidateDoesNotReadTooManyRoutes(t *testing.T) {
	a, st := newApp(t)
	req := ValidateRequest{}
	for i := range 5000 {
		req.Draft.Routes = append(req.Draft.Routes, DraftRoute{
			Pattern:     fmt.Sprintf("{bad%d:regex([a-z]{0,300})}/{b:regex([a-z]{0,300})}/{c:regex([a-z]{0,300})}", i),
			Destination: "nope",
		})
		req.Samples = append(req.Samples, map[string][]string{"b": {"x", "y"}})
	}
	calls := st.calls.Load()
	start := time.Now()
	res := a.Validate(context.Background(), localActor, req)
	took := time.Since(start)

	if res.Valid || len(res.Problems) != 1 || res.Problems[0].Code != linkmodel.CodeTooManyRoutes || res.Problems[0].Field != linkmodel.FieldLink {
		t.Fatalf("Validate = valid %v, problems %+v; want only %s", res.Valid, res.Problems, linkmodel.CodeTooManyRoutes)
	}
	if len(res.Routes) != 0 || res.Name.Value != "" || res.Name.Available {
		t.Errorf("the routes were read: %d route answers, name %+v", len(res.Routes), res.Name)
	}
	if took > 200*time.Millisecond {
		t.Errorf("Validate took %v for a draft that is refused at once", took)
	}
	if st.calls.Load() != calls {
		t.Error("Validate read the store")
	}
	// Saving refuses it the same way, and as quickly.
	start = time.Now()
	_, err := a.CreateLink(context.Background(), localActor, req.Draft)
	var invalid *ValidationError
	if !errors.As(err, &invalid) || len(invalid.Problems) != 1 || invalid.Problems[0].Code != linkmodel.CodeTooManyRoutes || len(invalid.Routes) != 0 {
		t.Errorf("CreateLink = %v, want only %s", err, linkmodel.CodeTooManyRoutes)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("CreateLink took %v for a draft that is refused at once", took)
	}
}
