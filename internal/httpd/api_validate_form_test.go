package httpd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// validRoutes answers a check of n routes as the real Service would for a
// draft without problems.
func validRoutes(name string, n int) func(app.ValidateRequest) app.ValidateResult {
	return func(req app.ValidateRequest) app.ValidateResult {
		return app.ValidateResult{
			Valid:  true,
			Name:   app.NameCheck{Value: name, Available: true},
			Routes: make([]app.RouteCheck, n),
		}
	}
}

// What the form asks for beyond the check: the variables of each route, with
// offsets in UTF-16 code units, and a regex's expression on its own.
func TestValidateDescribesVariables(t *testing.T) {
	svc := &fakeService{validateFn: validRoutes("a", 2)}
	rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", `{
		"describeVariables": true,
		"routes": [
			{"pattern": "a/{x:regex(ż😀+)}/{y:int}", "destination": "https://example.com/{x}/{y}"},
			{"pattern": "a", "destination": "https://example.com/"}
		]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	// "ż" is 2 bytes and 1 unit, "😀" 4 bytes and 2 units: x is bytes 2..20
	// and units 2..17; y follows at units 18..25.
	assertJSON(t, rec, `{
		"valid": true,
		"name": { "value": "a", "available": true, "existing": null, "canEdit": false },
		"routes": [
			{ "tokens": [], "problems": [], "samples": {},
			  "variables": [
				{ "name": "x", "type": "regex", "accepts": "text matching ż😀+", "expression": "ż😀+", "start": 2, "end": 17 },
				{ "name": "y", "type": "int", "accepts": "a whole number", "expression": "", "start": 18, "end": 25 } ] },
			{ "tokens": [], "problems": [], "samples": {}, "variables": [] }
		],
		"problems": [] }`)
}

// The live preview of an unsaved draft: the link model's decision for one
// request, with the reason sentences of the resolver.
func TestValidatePreview(t *testing.T) {
	routes := `"routes": [
		{"pattern": "reports/{month:int}/{day:int}", "destination": "https://reports.example.com/daily?month={month}&day={day}",
		 "variables": {"month": {"description": "Month"}}},
		{"pattern": "reports/{month:int}/{day", "destination": "https://reports.example.com/"}
	]`
	// The second route has an error; the fake says so as the Service would.
	invalidSecond := func(req app.ValidateRequest) app.ValidateResult {
		res := validRoutes("reports", 2)(req)
		res.Valid = false
		res.Routes[1].Problems = []linkmodel.Problem{{Severity: "error", Field: "pattern", Code: "pattern.unclosed_variable", Message: "Unclosed."}}
		return res
	}
	tests := []struct {
		name    string
		preview string
		want    string
	}{
		{"hit", `{"route": 0, "path": "reports/10/13"}`,
			`{"kind": "hit", "route": 0, "destination": "https://reports.example.com/daily?month=10&day=13", "values": {"month": "10", "day": "13"}}`},
		{"miss with the route's reason", `{"route": 0, "path": "reports/x/13"}`,
			`{"kind": "miss_known", "reasons": [ {"route": 0, "code": "value_rejected", "variable": "month", "value": "x",
				"accepts": "a whole number", "message": "month (Month) doesn't accept \"x\". It accepts a whole number."} ]}`},
		{"no route given", `{"path": "reports/x/13"}`,
			`{"kind": "miss_known", "reasons": [ {"route": 0, "code": "value_rejected", "variable": "month", "value": "x",
				"accepts": "a whole number", "message": "month (Month) doesn't accept \"x\". It accepts a whole number."} ]}`},
		{"the route asked about has errors", `{"route": 1, "path": "reports/10"}`,
			`{"kind": "invalid", "message": "Fix the problems of this route to see where it leads."}`},
		{"malformed path", `{"route": 0, "path": "reports/%zz/1"}`,
			`{"kind": "bad_request", "message": "This go link can't be read: a % in it is not followed by two hexadecimal digits, or it is not valid text."}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{validateFn: invalidSecond}
			rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate",
				`{"preview": `+tt.preview+`, `+routes+`}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
			}
			assertJSON(t, rec, `{
				"valid": false,
				"name": { "value": "reports", "available": true, "existing": null, "canEdit": false },
				"routes": [
					{ "tokens": [], "problems": [], "samples": {} },
					{ "tokens": [], "problems": [ { "severity": "error", "field": "pattern", "code": "pattern.unclosed_variable", "message": "Unclosed.", "start": 0, "end": 0 } ], "samples": {} }
				],
				"problems": [],
				"preview": `+tt.want+` }`)
			if len(svc.created)+len(svc.updated)+len(svc.deleted) != 0 {
				t.Errorf("a preview saved something")
			}
		})
	}
}

// The additions are checked like the rest of the body: a misspelt field is
// refused, not ignored.
func TestValidatePreviewRejectsBadRequests(t *testing.T) {
	for _, body := range []string{
		`{"routes": [], "preview": {"path": "a", "values": {}}}`,
		`{"routes": [], "preview": {"route": "0", "path": "a"}}`,
		`{"routes": [], "preview": "a/b"}`,
		`{"routes": [], "describeVariables": "yes"}`,
	} {
		svc := &fakeService{}
		rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != codeMalformed {
			t.Errorf("%s: status = %d, body %s; want 400 %s", body, rec.Code, rec.Body.String(), codeMalformed)
		}
		if svc.calls() != 0 {
			t.Errorf("%s: the Service was asked", body)
		}
	}
}

// What a launcher and the variables table ask about single values and
// expressions: the reason a refused sample would get, and an expression's
// problem in the terms of the expression alone (VAR-3, VAR-5).
func TestValidateSampleReasonsAndExpressions(t *testing.T) {
	svc := &fakeService{validateFn: func(req app.ValidateRequest) app.ValidateResult {
		res := validRoutes("rx", 1)(req)
		res.Routes[0].Samples = map[string]map[string]bool{"v": {"1": true, "x": false}}
		res.Routes[0].SampleReasons = map[string]map[string]string{"v": {"x": `v doesn't accept "x". It accepts a whole number.`}}
		return res
	}}
	rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", `{
		"explainSamples": true,
		"routes": [
			{"pattern": "rx/{v:int}", "destination": "https://example.com/{v}",
			 "samples": {"v": ["1", "x"]},
			 "expressions": {"a": "[0-9", "b": "(a|b", "c": "[0-9]+"}}
		]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	assertJSON(t, rec, `{
		"valid": true,
		"name": { "value": "rx", "available": true, "existing": null, "canEdit": false },
		"routes": [
			{ "tokens": [], "problems": [],
			  "samples": {"v": {"1": true, "x": false}},
			  "sampleReasons": {"v": {"x": "v doesn't accept \"x\". It accepts a whole number."}},
			  "expressions": {
				"a": {"valid": false, "code": "pattern.regex_invalid", "message": "The regular expression is not valid: a character class is opened with [ and never closed with ]."},
				"b": {"valid": false, "code": "pattern.regex_invalid", "message": "The regular expression is not valid: a parenthesis is opened and never closed."},
				"c": {"valid": true} } }
		],
		"problems": [] }`)

	// Without explainSamples the answer is the plan's (section 3.5).
	rec = postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", `{
		"routes": [{"pattern": "rx/{v:int}", "destination": "https://example.com/{v}", "samples": {"v": ["1", "x"]}}]}`)
	if body := rec.Body.String(); strings.Contains(body, "sampleReasons") || strings.Contains(body, "expressions") {
		t.Errorf("an answer not asked for: %s", body)
	}
}
