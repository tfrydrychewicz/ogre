package linkmodel

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The types below mirror testdata/spec.json, which transcribes PRD 5.7; the
// schema is described in testdata/README.txt. The file is decoded with
// unknown fields disallowed, so a row this test does not understand fails
// instead of being skipped.

type specFile struct {
	Format   string        `json:"format"`
	Version  int           `json:"version"`
	Source   string        `json:"source"`
	Links    []specLink    `json:"links"`
	Requests []specRequest `json:"requests"`
	Saves    []specSave    `json:"saves"`
}

type specRoute struct {
	Pattern     string `json:"pattern"`
	Destination string `json:"destination"`
}

type specLink struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Routes      []specRoute `json:"routes"`
}

type specRequest struct {
	ID        string       `json:"id"`
	Request   string       `json:"request"`
	Path      string       `json:"path"`
	Rule      string       `json:"rule"`
	Pending   string       `json:"pending"`
	UntilThen *specOutcome `json:"untilThen"`
	Expect    specOutcome  `json:"expect"`
}

type specOutcome struct {
	Kind          string            `json:"kind"`
	Link          string            `json:"link,omitempty"`
	Route         string            `json:"route,omitempty"`
	Values        map[string]string `json:"values,omitempty"`
	Destination   string            `json:"destination,omitempty"`
	Reasons       []specReason      `json:"reasons,omitempty"`
	RequestedName string            `json:"requestedName,omitempty"`
	Suggestions   []specSuggestion  `json:"suggestions,omitempty"`
}

type specReason struct {
	Route    string `json:"route"`
	Code     string `json:"code"`
	Variable string `json:"variable,omitempty"`
	Value    string `json:"value,omitempty"`
}

type specSuggestion struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Edits       int    `json:"edits"`
	Resolves    bool   `json:"resolves"`
	Description string `json:"description"`
	Destination string `json:"destination,omitempty"`
}

type specSave struct {
	ID      string `json:"id"`
	Attempt string `json:"attempt"`
	Outcome string `json:"outcome"`
	Needs   string `json:"needs"`
	Action  string `json:"action"`
	Link    string `json:"link"`
	Actor   string `json:"actor"`
	Draft   struct {
		Routes []specRoute `json:"routes"`
	} `json:"draft"`
	Expect specSaveExpect `json:"expect"`
}

type specSaveExpect struct {
	Saved      bool          `json:"saved"`
	Name       string        `json:"name"`
	Error      string        `json:"error"`
	Existing   string        `json:"existing"`
	NamesOwner bool          `json:"namesOwner"`
	LinksTo    string        `json:"linksTo"`
	Problems   []specProblem `json:"problems"`
	Then       []specThen    `json:"then"`
}

type specProblem struct {
	Route    int    `json:"route"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Mentions string `json:"mentions"`
}

type specThen struct {
	Path   string      `json:"path"`
	Expect specOutcome `json:"expect"`
}

func loadSpec(t testing.TB) specFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var spec specFile
	if err := dec.Decode(&spec); err != nil {
		t.Fatalf("testdata/spec.json: %v", err)
	}
	if spec.Format != "ogre-spec" || spec.Version != 1 {
		t.Fatalf("testdata/spec.json is %q version %d; this test reads ogre-spec version 1", spec.Format, spec.Version)
	}
	return spec
}

func (r specRoute) draft() DraftRoute {
	return DraftRoute{Pattern: r.Pattern, Destination: r.Destination}
}

func specDraft(description string, routes []specRoute) Draft {
	d := Draft{Description: description}
	for _, r := range routes {
		d.Routes = append(d.Routes, r.draft())
	}
	return d
}

// specDirectory is the smallest thing that resolves a path the way PRD 5.5
// and 5.6 say, built only from this package: split, look the name up,
// match, and for an unknown name suggest. The application's Resolve does the
// same against its index; replaying the fixture here proves the rules
// before anything is built on them.
type specDirectory map[string]CompiledLink

// newSpecDirectory saves the fixture links the way a real save would: each
// one must pass ValidateDraft without a single problem, warnings included.
func newSpecDirectory(t testing.TB, links []specLink) specDirectory {
	t.Helper()
	dir := specDirectory{}
	for _, l := range links {
		link, problems := ValidateDraft(specDraft(l.Description, l.Routes))
		if problems.Count() != 0 {
			t.Fatalf("fixture link %q does not validate cleanly: %+v", l.Name, problems)
		}
		if link.Name != l.Name {
			t.Fatalf("fixture link %q validated to the name %q", l.Name, link.Name)
		}
		for i := range link.Routes {
			link.Routes[i].ID = int64(len(dir)*100 + i + 1)
		}
		dir[link.Name] = Compile(link)
	}
	return dir
}

func (dir specDirectory) names() []string {
	names := make([]string, 0, len(dir))
	for name := range dir {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (dir specDirectory) resolve(t testing.TB, path string) specOutcome {
	t.Helper()
	segments, err := SplitPath(path)
	if err != nil {
		return specOutcome{Kind: "bad_request"}
	}
	if len(segments) == 0 {
		return specOutcome{Kind: "home"}
	}
	name := NormalizeName(segments[0])
	link, found := dir[name]
	if !found {
		out := specOutcome{Kind: "miss_unknown", RequestedName: name}
		for _, s := range SuggestNames(name, dir.names(), SuggestOptions{}) {
			candidate := dir[s.Name]
			suggestion := specSuggestion{
				Name: s.Name, Path: s.Name, Edits: s.Edits,
				Description: candidate.Link.Description,
			}
			corrected := append([]string{s.Name}, segments[1:]...)
			if m, ok, _, err := candidate.Match(corrected); err != nil {
				t.Fatalf("Match(%q): %v", corrected, err)
			} else if ok {
				suggestion.Path = strings.Join(corrected, "/")
				suggestion.Resolves, suggestion.Destination = true, m.Destination
			}
			out.Suggestions = append(out.Suggestions, suggestion)
		}
		return out
	}
	m, ok, reasons, err := link.Match(segments)
	if err != nil {
		t.Fatalf("Match(%q): %v", segments, err)
	}
	if ok {
		if reasons != nil {
			t.Errorf("Match(%q) returned reasons with a hit: %+v", segments, reasons)
		}
		return specOutcome{
			Kind: "hit", Link: link.Link.Name, Route: m.Route.Pattern,
			Values: m.Values, Destination: m.Destination,
		}
	}
	out := specOutcome{Kind: "miss_known", Link: link.Link.Name}
	for i, r := range reasons {
		if r.RouteID != link.Link.Routes[i].ID || r.Pattern != link.Link.Routes[i].Pattern {
			t.Errorf("reason %d is for route %d %q, want the link's route %d", i, r.RouteID, r.Pattern, i)
		}
		// The sentence is not in the fixture, but it must be one, and must
		// name what the PRD says it names.
		if r.Message == "" || !strings.HasSuffix(r.Message, ".") ||
			!strings.Contains(r.Message, r.Variable) || !strings.Contains(r.Message, r.Value) {
			t.Errorf("reason %+v: the message does not name the variable and the value", r)
		}
		if r.Variable != "" && (r.Accepts == "" || !strings.Contains(r.Message, r.Accepts)) {
			t.Errorf("reason %+v: the message does not say what the variable accepts", r)
		}
		out.Reasons = append(out.Reasons, specReason{Route: r.Pattern, Code: r.Code, Variable: r.Variable, Value: r.Value})
	}
	return out
}

// sameOutcome compares two outcomes, treating an empty map or list the same
// whether it is written out or left out.
func sameOutcome(got, want specOutcome) bool {
	for _, o := range []*specOutcome{&got, &want} {
		if len(o.Values) == 0 {
			o.Values = nil
		}
		if len(o.Reasons) == 0 {
			o.Reasons = nil
		}
		if len(o.Suggestions) == 0 {
			o.Suggestions = nil
		}
	}
	return reflect.DeepEqual(got, want)
}

// TestSpecRequests replays every request of PRD 5.7 (NFR-22).
func TestSpecRequests(t *testing.T) {
	spec := loadSpec(t)
	dir := newSpecDirectory(t, spec.Links)

	seen := map[string]bool{}
	for _, req := range spec.Requests {
		seen[req.ID] = true
		t.Run(req.ID+" "+req.Request, func(t *testing.T) {
			want := req.Expect
			if req.Pending != "" {
				if req.UntilThen == nil {
					t.Fatalf("row %s is pending %s but says nothing about what happens until then", req.ID, req.Pending)
				}
				want = *req.UntilThen
				defer t.Skipf("PRD outcome is pending %s; checked what happens until then (%s)",
					req.Pending, want.Destination)
			}
			if got := dir.resolve(t, req.Path); !sameOutcome(got, want) {
				t.Errorf("%s resolved to\n  %+v\nwant\n  %+v\n(%s)", req.Request, got, want, req.Rule)
			}
		})
	}
	// The PRD table has rows 1 to 18, and 15a twice.
	for _, id := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14",
		"15", "15a.1", "15a.2", "16", "17", "18"} {
		if !seen[id] {
			t.Errorf("spec.json has no request row %s", id)
		}
	}
	if len(spec.Requests) != 20 {
		t.Errorf("spec.json has %d request rows, want 20", len(spec.Requests))
	}
}

// TestSpecSaves replays the save attempts of PRD 5.7 as far as the link
// model decides them. Rows marked "app" need stored links or a second user;
// here only their draft is checked, and the application's test finishes
// them.
func TestSpecSaves(t *testing.T) {
	spec := loadSpec(t)
	if len(spec.Saves) != 9 {
		t.Errorf("spec.json has %d save rows, want the 9 of the PRD table", len(spec.Saves))
	}
	for _, save := range spec.Saves {
		t.Run(save.ID+" "+save.Attempt, func(t *testing.T) {
			link, problems := ValidateDraft(specDraft("", save.Draft.Routes))

			switch {
			case save.Needs == "app":
				// The link model has no objection; what refuses the save, if
				// anything does, is the name or the actor.
				if problems.HasErrors() || link.Name != save.Expect.Name {
					t.Fatalf("ValidateDraft = name %q, problems %+v; want a valid draft named %q",
						link.Name, problems, save.Expect.Name)
				}
				if save.Expect.Error == "validation" {
					t.Fatalf("row %s expects a validation error but is marked as needing the app", save.ID)
				}
			case save.Needs != "linkmodel":
				t.Fatalf("row %s needs %q, which this test does not know", save.ID, save.Needs)
			case save.Expect.Saved:
				if problems.HasErrors() || link.Name != save.Expect.Name {
					t.Fatalf("ValidateDraft = name %q, problems %+v; want a valid draft named %q",
						link.Name, problems, save.Expect.Name)
				}
			default:
				if save.Expect.Error != "validation" {
					t.Fatalf("row %s expects error %q, which the link model cannot decide", save.ID, save.Expect.Error)
				}
				var got []specProblem
				for i, route := range problems.Routes {
					for _, p := range route {
						if p.Severity == SeverityError {
							got = append(got, specProblem{Route: i, Field: p.Field, Code: p.Code})
						}
					}
				}
				var want []specProblem
				for _, p := range save.Expect.Problems {
					want = append(want, specProblem{Route: p.Route, Field: p.Field, Code: p.Code})
				}
				if len(problems.Link) != 0 || !reflect.DeepEqual(got, want) {
					t.Fatalf("problems =\n  link %+v\n  routes %+v\nwant\n  %+v", problems.Link, problems.Routes, want)
				}
				for _, p := range save.Expect.Problems {
					for _, found := range problems.Routes[p.Route] {
						if found.Code == p.Code && !strings.Contains(found.Message, p.Mentions) {
							t.Errorf("message %q does not mention %q", found.Message, p.Mentions)
						}
					}
				}
				if link.Name != "" || link.Routes != nil {
					t.Errorf("ValidateDraft returned link %+v with errors", link)
				}
			}

			if len(save.Expect.Then) == 0 {
				return
			}
			// What the save changes: the fixture links, with this one
			// replaced or added.
			dir := newSpecDirectory(t, spec.Links)
			for i := range link.Routes {
				link.Routes[i].ID = int64(9000 + i)
			}
			dir[link.Name] = Compile(link)
			for _, then := range save.Expect.Then {
				if got := dir.resolve(t, then.Path); !sameOutcome(got, then.Expect) {
					t.Errorf("after the save, %q resolved to\n  %+v\nwant\n  %+v", then.Path, got, then.Expect)
				}
			}
		})
	}
}

// The fixture is the PRD's text; a typo in a pattern there would silently
// test something else. Every pattern in it must already be canonical.
func TestSpecPatternsAreCanonical(t *testing.T) {
	spec := loadSpec(t)
	for _, l := range spec.Links {
		for _, r := range l.Routes {
			p, problems := ParsePattern(r.Pattern)
			if len(problems) != 0 || p.String() != r.Pattern || p.Name() != l.Name {
				t.Errorf("fixture pattern %q of link %q: canonical %q, problems %+v", r.Pattern, l.Name, p.String(), problems)
			}
		}
	}
}
