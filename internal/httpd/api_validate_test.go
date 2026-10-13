package httpd

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// The example of plan section 3.5, answered by a fake that returns what the
// link model reports for it. The offsets here are bytes, as the link model
// counts; the pattern is ASCII, so the API's UTF-16 offsets are the same.
func TestValidate(t *testing.T) {
	svc := &fakeService{validateFn: func(app.ValidateRequest) app.ValidateResult {
		return app.ValidateResult{
			Valid: false,
			Name:  app.NameCheck{Value: "reports", Available: true},
			Routes: []app.RouteCheck{{
				Tokens: []linkmodel.Token{
					{Kind: "literal", Start: 0, End: 7},
					{Kind: "variable", Start: 8, End: 19, Name: "month", Type: "int"},
				},
				Problems: []linkmodel.Problem{{
					Severity: "error", Field: "pattern", Code: "pattern.unclosed_variable",
					Message: "This variable is missing its closing }.", Start: 20, End: 24,
				}},
				Samples: map[string]map[string]bool{"month": {"10": true, "x": false}},
			}},
		}
	}}
	rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", `{
		"linkId": null,
		"routes": [ { "pattern": "reports/{month:int}/{day",
		              "destination": "https://reports.example.com/daily?month={month}&day={day}",
		              "samples": { "month": ["10", "x"] } } ] }`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	assertJSON(t, rec, `{
		"valid": false,
		"name": { "value": "reports", "available": true, "existing": null, "canEdit": false },
		"routes": [ {
			"tokens": [ { "kind": "literal", "start": 0, "end": 7 },
			            { "kind": "variable", "start": 8, "end": 19, "name": "month", "type": "int" } ],
			"problems": [ { "severity": "error", "field": "pattern", "code": "pattern.unclosed_variable",
			                "start": 20, "end": 24, "message": "This variable is missing its closing }." } ],
			"samples": { "month": { "10": true, "x": false } } } ],
		"problems": [] }`)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	assertNoCORS(t, rec.Header())

	want := app.ValidateRequest{
		LinkID: 0,
		Draft: app.Draft{Routes: []app.DraftRoute{{
			Pattern:     "reports/{month:int}/{day",
			Destination: "https://reports.example.com/daily?month={month}&day={day}",
		}}},
		Samples: []map[string][]string{{"month": {"10", "x"}}},
	}
	if len(svc.validated) != 1 || !reflect.DeepEqual(svc.validated[0], want) {
		t.Errorf("request = %+v\nwant      %+v", svc.validated, want)
	}
	if len(svc.actors) != 1 || svc.actors[0] != testActor {
		t.Errorf("actor = %+v, want %+v", svc.actors, testActor)
	}
	if len(svc.created)+len(svc.updated) != 0 {
		t.Errorf("validate saved something")
	}
}

// Everything a result can carry, including what the plan's example leaves
// empty: a taken name, link-level problems, warnings, several routes, and
// offsets after non-ASCII text.
func TestValidateFullResult(t *testing.T) {
	existing := linkmodel.Link{ID: 4, Name: "żó", Description: "Taken", OwnerID: 2}
	svc := &fakeService{validateFn: func(app.ValidateRequest) app.ValidateResult {
		return app.ValidateResult{
			Valid: false,
			Name:  app.NameCheck{Value: "żó", Available: false, Existing: &existing, ExistingEditable: true},
			Routes: []app.RouteCheck{
				{
					// "żó/{repo}": "ż" and "ó" are two bytes and one UTF-16
					// unit each, so byte 4 is unit 2 and byte 11 is unit 9.
					Tokens: []linkmodel.Token{
						{Kind: "literal", Start: 0, End: 4},
						{Kind: "variable", Start: 5, End: 11, Name: "repo", Type: "string"},
					},
					Problems: []linkmodel.Problem{
						{Severity: "error", Field: "pattern", Code: "pattern.bad_character", Message: "Bad.", Start: 0, End: 2},
						// "https://é.example/😀{x}": "é" is 2 bytes and 1
						// unit, "😀" 4 bytes and 2 units; the placeholder is
						// at bytes 23..26, units 20..23.
						{Severity: "error", Field: "destination", Code: "destination.undeclared_variable", Message: "Undeclared.", Start: 23, End: 26},
					},
				},
				{}, // a route with nothing to report
				{
					Tokens:   []linkmodel.Token{{Kind: "literal", Start: 0, End: 2}},
					Problems: []linkmodel.Problem{{Severity: "warning", Field: "destination", Code: "destination.unused_variable", Message: "Unused.", Start: 19, End: 19}},
					Samples:  map[string]map[string]bool{},
				},
			},
			Problems: []linkmodel.Problem{{Severity: "error", Field: "pattern", Code: "pattern.too_many_routes", Message: "Too many."}},
		}
	}}
	rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", `{
		"linkId": 12,
		"description": "Edited",
		"routes": [
			{"id": 31, "pattern": "żó/{repo}", "destination": "https://é.example/😀{x}", "variables": {"repo": {"description": "Repository"}}},
			{"pattern": "żó/b", "destination": "https://example.com/b", "samples": {"x": []}},
			{"pattern": "gh", "destination": "https://example.com"}
		]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	assertJSON(t, rec, `{
		"valid": false,
		"name": { "value": "żó", "available": false, "existing": { "id": 4, "name": "żó" }, "canEdit": true },
		"routes": [
			{ "tokens": [ { "kind": "literal", "start": 0, "end": 2 },
			              { "kind": "variable", "start": 3, "end": 9, "name": "repo", "type": "string" } ],
			  "problems": [
				{ "severity": "error", "field": "pattern", "code": "pattern.bad_character", "message": "Bad.", "start": 0, "end": 1 },
				{ "severity": "error", "field": "destination", "code": "destination.undeclared_variable", "message": "Undeclared.", "start": 20, "end": 23 } ],
			  "samples": {} },
			{ "tokens": [], "problems": [], "samples": {} },
			{ "tokens": [ { "kind": "literal", "start": 0, "end": 2 } ],
			  "problems": [ { "severity": "warning", "field": "destination", "code": "destination.unused_variable", "message": "Unused.", "start": 19, "end": 19 } ],
			  "samples": {} }
		],
		"problems": [ { "severity": "error", "field": "pattern", "code": "pattern.too_many_routes", "message": "Too many.", "start": 0, "end": 0 } ] }`)

	want := app.ValidateRequest{
		LinkID: 12,
		Draft: app.Draft{Description: "Edited", Routes: []app.DraftRoute{
			{ID: 31, Pattern: "żó/{repo}", Destination: "https://é.example/😀{x}",
				Variables: []linkmodel.VariableMeta{{Name: "repo", Description: "Repository"}}},
			{Pattern: "żó/b", Destination: "https://example.com/b"},
			{Pattern: "gh", Destination: "https://example.com"},
		}},
		// One entry per route as soon as any route has samples.
		Samples: []map[string][]string{nil, {"x": {}}, nil},
	}
	if len(svc.validated) != 1 || !reflect.DeepEqual(svc.validated[0], want) {
		t.Errorf("request = %+v\nwant      %+v", svc.validated, want)
	}
}

// A valid draft: every list is present and empty, never null.
func TestValidateEmptyResult(t *testing.T) {
	for _, body := range []string{`{}`, `{"routes":[]}`, `{"linkId":0,"routes":null}`} {
		svc := &fakeService{}
		rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links/validate", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", body, rec.Code)
		}
		assertJSON(t, rec, `{"valid":true,"name":{"value":"","available":false,"existing":null,"canEdit":false},"routes":[],"problems":[]}`)
		if len(svc.validated) != 1 || svc.validated[0].Samples != nil || svc.validated[0].LinkID != 0 {
			t.Errorf("%s: request = %+v", body, svc.validated)
		}
	}
}

func TestValidateRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{"not JSON", "application/json", `{"routes":`, 400, codeMalformed},
		{"unknown field", "application/json", `{"routes":[],"name":"x"}`, 400, codeMalformed},
		{"version is not part of a check", "application/json", `{"routes":[],"version":1}`, 400, codeMalformed},
		{"unknown route field", "application/json", `{"routes":[{"pattern":"a","destination":"b","sample":{}}]}`, 400, codeMalformed},
		{"samples of the wrong type", "application/json", `{"routes":[{"pattern":"a","destination":"b","samples":{"x":"10"}}]}`, 400, codeMalformed},
		{"link ID as text", "application/json", `{"linkId":"12","routes":[]}`, 400, codeMalformed},
		{"plain text", "text/plain", `{"routes":[]}`, 415, codeUnsupportedMediaType},
		{"no content type", "", `{"routes":[]}`, 415, codeUnsupportedMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			headers := map[string]string{}
			if tt.contentType != "" {
				headers["Content-Type"] = tt.contentType
			}
			rec := request{method: http.MethodPost, target: "/-/api/v1/links/validate", headers: headers, body: tt.body}.send(newTestServer(svc, nil).Handler())
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			if svc.calls() != 0 {
				t.Errorf("a refused request reached the Service")
			}
		})
	}

	// Validate writes nothing, but it is a POST and answers with data about
	// the Directory: the cross-site guards apply to it like to any write.
	t.Run("cross-site", func(t *testing.T) {
		svc := &fakeService{}
		rec := request{method: http.MethodPost, target: "/-/api/v1/links/validate", body: `{"routes":[]}`, headers: map[string]string{
			"Content-Type": "application/json", "Origin": "https://evil.example",
		}}.send(newTestServer(svc, nil).Handler())
		if rec.Code != http.StatusForbidden || svc.calls() != 0 {
			t.Errorf("status = %d, calls = %d; want 403 and none", rec.Code, svc.calls())
		}
	})
}
