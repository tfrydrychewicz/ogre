package httpd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// variableLink is a link with typed variables, one of them described. The
// month pattern is PRD 5.7's.
func variableLink() linkmodel.Link {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return linkmodel.Link{
		ID: 12, Name: "gh", Description: "GitHub shortcuts", OwnerID: 1, Version: 3,
		Routes: []linkmodel.Route{
			{
				ID: 31, Position: 0,
				Pattern:     "gh/{repo}/pr/{num:int}",
				Destination: "https://github.com/acme/{repo}/pull/{num}",
				Variables: []linkmodel.VariableMeta{
					{Name: "repo", Description: "Repository", Example: "ogre"},
					{Name: "num"},
				},
			},
			{
				ID: 32, Position: 1,
				Pattern:     "gh/stats/{month:regex(^(0?[1-9]|1[0-2])$)}/{ratio:number}",
				Destination: "https://stats.example.com/?m={month}&r={ratio}",
				// Nothing stored about the variables: both still appear.
			},
			{ID: 33, Position: 2, Pattern: "gh", Destination: "https://github.com/acme"},
		},
		CreatedAt: at, UpdatedAt: at,
	}
}

// The link of plan section 3.5, extended by a regex and a number variable.
const variableLinkJSON = `{
  "id": 12,
  "name": "gh",
  "description": "GitHub shortcuts",
  "owner": { "id": 1, "displayName": "Local user" },
  "version": 3,
  "canEdit": true,
  "routes": [
    { "id": 31, "pattern": "gh/{repo}/pr/{num:int}", "destination": "https://github.com/acme/{repo}/pull/{num}",
      "variables": [
        { "name": "repo", "type": "string", "accepts": "any text", "description": "Repository", "example": "ogre" },
        { "name": "num", "type": "int", "accepts": "a whole number", "description": "", "example": "" } ] },
    { "id": 32, "pattern": "gh/stats/{month:regex(^(0?[1-9]|1[0-2])$)}/{ratio:number}",
      "destination": "https://stats.example.com/?m={month}&r={ratio}",
      "variables": [
        { "name": "month", "type": "regex", "accepts": "text matching ^(0?[1-9]|1[0-2])$", "description": "", "example": "" },
        { "name": "ratio", "type": "number", "accepts": "a number such as 42 or 3.5", "description": "", "example": "" } ] },
    { "id": 33, "pattern": "gh", "destination": "https://github.com/acme", "variables": [] }
  ],
  "usage": { "total": 0, "last30Days": 0, "lastUsedAt": null },
  "createdAt": "2026-10-13T09:00:00Z",
  "updatedAt": "2026-10-13T09:00:00Z"
}`

func TestGetLink(t *testing.T) {
	svc := &fakeService{getFn: func(int64) (linkmodel.Link, error) { return variableLink(), nil }}
	h := newTestServer(svc, nil).Handler()

	rec := get(h, "/-/api/v1/links/12")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	assertJSON(t, rec, variableLinkJSON)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	assertNoCORS(t, rec.Header())

	head := request{method: http.MethodHead, target: "/-/api/v1/links/12"}.send(h)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD status = %d with %d body bytes, want 200 and none", head.Code, head.Body.Len())
	}
	if !reflect.DeepEqual(head.Header(), rec.Header()) {
		t.Errorf("HEAD headers differ from GET:\n GET  %v\n HEAD %v", rec.Header(), head.Header())
	}

	if want := []int64{12, 12}; !reflect.DeepEqual(svc.got, want) {
		t.Errorf("asked for %v, want %v", svc.got, want)
	}
	for _, actor := range svc.actors {
		if actor != testActor {
			t.Errorf("actor = %+v, want %+v", actor, testActor)
		}
	}
}

// A stored pattern the link model cannot read still lists the link: the
// route simply has no variables to show.
func TestLinkJSONWithUnreadablePattern(t *testing.T) {
	link := sampleLink()
	link.Routes = []linkmodel.Route{{ID: 1, Pattern: "roadmap/{broken", Destination: "https://example.com"}}
	svc := &fakeService{getFn: func(int64) (linkmodel.Link, error) { return link, nil }}

	rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/links/12")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"variables":[]`) {
		t.Errorf("status = %d, body = %s; want 200 with empty variables", rec.Code, rec.Body.String())
	}
}

// An ID is decimal digits. Everything else is an address where no link
// lives: 404, the same as for a missing link, without asking the Service.
func TestLinkIDParsing(t *testing.T) {
	notIDs := []string{
		"abc", "-1", "+5", "0", "00", "1.5", "1e3", "0x10", "12abc", "%201", "1%20", "%2D1",
		"9223372036854775808",           // one past the largest ID
		"99999999999999999999999999999", // overflow
		"%D9%A1%D9%A2",                  // Arabic-Indic digits
		"12%2F13",                       // an encoded slash stays inside the segment
		"validate%20",                   // not the validate endpoint either
	}
	methods := []string{http.MethodGet, http.MethodHead, http.MethodPatch, http.MethodDelete}
	for _, id := range notIDs {
		for _, method := range methods {
			t.Run(method+" "+id, func(t *testing.T) {
				svc := &fakeService{}
				rec := sendJSON(newTestServer(svc, nil).Handler(), method, "/-/api/v1/links/"+id, `{"description":"","routes":[],"version":1}`)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
				}
				if method != http.MethodHead {
					if got := errorCode(t, rec); got != "link.not_found" {
						t.Errorf("code = %q, want link.not_found", got)
					}
				}
				if svc.calls() != 0 {
					t.Errorf("the Service was asked about %q", id)
				}
			})
		}
	}

	ids := map[string]int64{"1": 1, "12": 12, "007": 7, "9223372036854775807": 9223372036854775807}
	for text, want := range ids {
		t.Run("valid "+text, func(t *testing.T) {
			svc := &fakeService{}
			rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/links/"+text)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if !reflect.DeepEqual(svc.got, []int64{want}) {
				t.Errorf("asked for %v, want %d", svc.got, want)
			}
		})
	}
}

func TestUpdateLink(t *testing.T) {
	svc := &fakeService{updateFn: func(int64, app.Draft, int) (linkmodel.Link, error) { return variableLink(), nil }}
	h := newTestServer(svc, nil).Handler()

	rec := sendJSON(h, http.MethodPatch, "/-/api/v1/links/12", `{
		"description": "GitHub shortcuts",
		"routes": [
			{"id": 31, "pattern": "GH/{repo}/pr/{num:int}", "destination": "https://github.com/acme/{repo}/pull/{num}",
			 "variables": {"repo": {"description": "Repository", "example": "ogre"}, "num": {"description": "PR number"}, "ghost": {}}},
			{"pattern": "gh", "destination": "https://github.com/acme"},
			{"id": null, "pattern": "gh/new", "destination": "https://github.com/new", "variables": {}}
		],
		"version": 3
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	assertJSON(t, rec, variableLinkJSON)
	assertNoCORS(t, rec.Header())

	// The draft reaches the Service as written. Variables arrive sorted by
	// name, because a JSON object has no order; an entry for a variable the
	// pattern lacks ("ghost") is passed on for the Service to refuse.
	want := updateCall{
		id:      12,
		version: 3,
		draft: app.Draft{
			Description: "GitHub shortcuts",
			Routes: []app.DraftRoute{
				{ID: 31, Pattern: "GH/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}",
					Variables: []linkmodel.VariableMeta{
						{Name: "ghost"},
						{Name: "num", Description: "PR number"},
						{Name: "repo", Description: "Repository", Example: "ogre"},
					}},
				{Pattern: "gh", Destination: "https://github.com/acme"},
				{Pattern: "gh/new", Destination: "https://github.com/new"},
			},
		},
	}
	if len(svc.updated) != 1 || !reflect.DeepEqual(svc.updated[0], want) {
		t.Errorf("update = %+v\nwant     %+v", svc.updated, want)
	}
	if len(svc.actors) != 1 || svc.actors[0] != testActor {
		t.Errorf("actor = %+v, want %+v", svc.actors, testActor)
	}
}

// POST takes the same routes as PATCH: IDs and variable descriptions reach
// the Service, which decides what an ID means for a new link.
func TestCreateLinkPassesRouteIDsAndVariables(t *testing.T) {
	svc := &fakeService{}
	rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links",
		`{"routes":[{"id":5,"pattern":"gh/{repo}","destination":"https://github.com/acme/{repo}","variables":{"repo":{"example":"ogre"}}}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	want := app.Draft{Routes: []app.DraftRoute{{ID: 5, Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}",
		Variables: []linkmodel.VariableMeta{{Name: "repo", Example: "ogre"}}}}}
	if got := svc.createdDrafts(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("draft = %+v, want %+v", got, want)
	}
}

func TestUpdateLinkRejectsBadBodies(t *testing.T) {
	const routes = `"routes":[{"pattern":"gh","destination":"https://github.com/acme"}]`
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
		wantInMsg  string
	}{
		{"no version", `{"description":"",` + routes + `}`, 400, codeMalformed, "version"},
		{"null version", `{"description":"",` + routes + `,"version":null}`, 400, codeMalformed, "version"},
		{"version zero", `{"description":"",` + routes + `,"version":0}`, 400, codeMalformed, "version"},
		{"negative version", `{"description":"",` + routes + `,"version":-2}`, 400, codeMalformed, "version"},
		{"version as text", `{"description":"",` + routes + `,"version":"3"}`, 400, codeMalformed, "version"},
		{"fractional version", `{"description":"",` + routes + `,"version":1.5}`, 400, codeMalformed, "version"},
		{"huge version", `{"description":"",` + routes + `,"version":99999999999999999999}`, 400, codeMalformed, "version"},
		{"no description", `{` + routes + `,"version":1}`, 400, codeMalformed, "description"},
		{"null description", `{"description":null,` + routes + `,"version":1}`, 400, codeMalformed, "description"},
		{"no routes", `{"description":"","version":1}`, 400, codeMalformed, "routes"},
		{"null routes", `{"description":"","routes":null,"version":1}`, 400, codeMalformed, "routes"},
		{"name is never sent", `{"name":"gh","description":"",` + routes + `,"version":1}`, 400, codeMalformed, "name"},
		{"unknown field", `{"description":"",` + routes + `,"version":1,"owner":2}`, 400, codeMalformed, "owner"},
		{"unknown route field", `{"description":"","routes":[{"pattern":"gh","destination":"https://x.example","position":1}],"version":1}`, 400, codeMalformed, "position"},
		{"samples belong to validate", `{"description":"","routes":[{"pattern":"gh","destination":"https://x.example","samples":{}}],"version":1}`, 400, codeMalformed, "samples"},
		{"unknown variable field", `{"description":"","routes":[{"pattern":"gh/{a}","destination":"https://x.example/{a}","variables":{"a":{"type":"int"}}}],"version":1}`, 400, codeMalformed, "type"},
		{"variables as a list", `{"description":"","routes":[{"pattern":"gh/{a}","destination":"https://x.example/{a}","variables":[{"name":"a"}]}],"version":1}`, 400, codeMalformed, "variables"},
		{"route id as text", `{"description":"","routes":[{"id":"31","pattern":"gh","destination":"https://x.example"}],"version":1}`, 400, codeMalformed, "id"},
		{"not JSON", `{"description":`, 400, codeMalformed, ""},
		{"empty body", ``, 400, codeMalformed, ""},
		{"an array", `[]`, 400, codeMalformed, ""},
		{"two values", `{"description":"",` + routes + `,"version":1}{}`, 400, codeMalformed, "extra"},
		{"too large", `{"description":"` + strings.Repeat("a", maxBodyBytes) + `",` + routes + `,"version":1}`, 413, codeTooLarge, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			rec := sendJSON(newTestServer(svc, nil).Handler(), http.MethodPatch, "/-/api/v1/links/12", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %.300s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantInMsg) {
				t.Errorf("body %s does not mention %q", rec.Body.String(), tt.wantInMsg)
			}
			if svc.calls() != 0 {
				t.Errorf("a refused body reached the Service")
			}
		})
	}

	// The same field rules hold for POST, which has no version at all.
	t.Run("POST with a version", func(t *testing.T) {
		svc := &fakeService{}
		rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links", `{`+routes+`,"version":1}`)
		if rec.Code != http.StatusBadRequest || svc.calls() != 0 {
			t.Errorf("status = %d, calls = %d; want 400 and none", rec.Code, svc.calls())
		}
	})
}

// An update with an empty list of routes is a well-formed request about a
// link that cannot be saved; saying so is the Service's business.
func TestUpdateLinkLeavesValidationToTheService(t *testing.T) {
	svc := &fakeService{}
	rec := sendJSON(newTestServer(svc, nil).Handler(), http.MethodPatch, "/-/api/v1/links/12",
		`{"description":"","routes":[],"version":1}`)
	if rec.Code != http.StatusOK || len(svc.updated) != 1 {
		t.Errorf("status = %d, updates = %d; want the request passed on", rec.Code, len(svc.updated))
	}
}

func TestDeleteLink(t *testing.T) {
	tests := []struct {
		target      string
		wantVersion int
	}{
		{"/-/api/v1/links/12", 0},
		{"/-/api/v1/links/12?version=3", 3},
		{"/-/api/v1/links/12?other=1&version=7", 7},
		{"/-/api/v1/links/12?other=1", 0},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			svc := &fakeService{}
			// No headers at all: what curl -X DELETE sends.
			rec := request{method: http.MethodDelete, target: tt.target}.send(newTestServer(svc, nil).Handler())
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
			}
			if rec.Body.Len() != 0 {
				t.Errorf("a 204 has a body: %q", rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "" {
				t.Errorf("Content-Type = %q on a response without a body", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			assertNoCORS(t, rec.Header())
			if want := []deleteCall{{12, tt.wantVersion}}; !reflect.DeepEqual(svc.deleted, want) {
				t.Errorf("deleted %+v, want %+v", svc.deleted, want)
			}
			if svc.actors[0] != testActor {
				t.Errorf("actor = %+v", svc.actors[0])
			}
		})
	}
}

// A version that was sent but cannot be used must not fall back to an
// unconditional delete.
func TestDeleteLinkRejectsBadVersions(t *testing.T) {
	for _, query := range []string{
		"version=", "version=abc", "version=0", "version=-1", "version=1.5", "version=+1x",
		"version=99999999999999999999", "version=1&version=1", "version=1&version=2",
		"version=%zz", "version=1;x=2", "version",
	} {
		t.Run(query, func(t *testing.T) {
			svc := &fakeService{}
			rec := request{method: http.MethodDelete, target: "/-/api/v1/links/12?" + query}.send(newTestServer(svc, nil).Handler())
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
			if got := errorCode(t, rec); got != codeMalformed {
				t.Errorf("code = %q, want %q", got, codeMalformed)
			}
			if svc.calls() != 0 {
				t.Errorf("the link was deleted although the version was unusable")
			}
		})
	}
}

// Every error a use case may return, on every endpoint that can meet it.
func TestLinkErrorMapping(t *testing.T) {
	existing := linkmodel.Link{ID: 4, Name: "reports", Description: "Daily reports", OwnerID: 2}
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantDetails string
	}{
		{"not found", app.ErrNotFound, 404, "link.not_found", `{}`},
		{"not found, wrapped", fmt.Errorf("get: %w", app.ErrNotFound), 404, "link.not_found", `{}`},
		{"forbidden", app.ErrForbidden, 403, "link.forbidden", `{}`},
		{"forbidden, naming the link and its owner", &app.ForbiddenError{Link: existing, Owner: "Ada Lovelace"}, 403, "link.forbidden",
			`{"link":{"id":4,"name":"reports"},"owner":{"id":2,"displayName":"Ada Lovelace"}}`},
		{"forbidden, the owner unknown", fmt.Errorf("save: %w", &app.ForbiddenError{Link: existing}), 403, "link.forbidden",
			`{"link":{"id":4,"name":"reports"},"owner":{"id":2,"displayName":""}}`},
		{"name taken, editable", &app.NameTakenError{Existing: existing, Editable: true}, 409, "link.name_taken",
			`{"existing":{"id":4,"name":"reports"},"canEdit":true}`},
		{"name taken, not editable", fmt.Errorf("save: %w", &app.NameTakenError{Existing: existing}), 409, "link.name_taken",
			`{"existing":{"id":4,"name":"reports"},"canEdit":false}`},
		{"name taken, nothing known", app.ErrNameTaken, 409, "link.name_taken", `{}`},
		{"version conflict", &app.VersionConflictError{CurrentVersion: 5}, 409, "link.version_conflict", `{"currentVersion":5}`},
		{"version conflict, wrapped", fmt.Errorf("save: %w", &app.VersionConflictError{CurrentVersion: 9}), 409, "link.version_conflict", `{"currentVersion":9}`},
		{"version conflict, nothing known", app.ErrVersionConflict, 409, "link.version_conflict", `{}`},
		{"validation", &app.ValidationError{
			Problems: []linkmodel.Problem{{Severity: "error", Field: "pattern", Code: "pattern.too_many_routes", Message: "Too many."}},
			Routes: [][]linkmodel.Problem{
				// "żó/{repo}": byte offsets 5..11 are UTF-16 offsets 3..9.
				{{Severity: "error", Field: "pattern", Code: "pattern.unknown_type", Message: "No such type.", Start: 5, End: 11}},
				{{Severity: "warning", Field: "destination", Code: "destination.unused_variable", Message: "Unused.", Start: 21, End: 21}},
			}}, 422, "link.invalid",
			`{"problems":[{"severity":"error","field":"pattern","code":"pattern.too_many_routes","message":"Too many.","start":0,"end":0}],
			  "routes":[
			    {"problems":[{"severity":"error","field":"pattern","code":"pattern.unknown_type","message":"No such type.","start":3,"end":9}]},
			    {"problems":[{"severity":"warning","field":"destination","code":"destination.unused_variable","message":"Unused.","start":21,"end":21}]}]}`},
		{"anything else", errors.New("disk I/O error: /secret/path/ogre.db"), 500, "internal", `{}`},
	}
	const body = `{"description":"","routes":[
		{"pattern":"żó/{repo}","destination":"https://example.com/1"},
		{"pattern":"gh/new","destination":"https://example.com/2"}],"version":1}`
	endpoints := []struct {
		method, target string
	}{
		{http.MethodGet, "/-/api/v1/links/12"},
		{http.MethodPatch, "/-/api/v1/links/12"},
		{http.MethodDelete, "/-/api/v1/links/12?version=1"},
	}
	for _, ep := range endpoints {
		for _, tt := range tests {
			t.Run(ep.method+" "+tt.name, func(t *testing.T) {
				logger, logs := captureLog()
				svc := &fakeService{
					getFn:    func(int64) (linkmodel.Link, error) { return linkmodel.Link{}, tt.err },
					updateFn: func(int64, app.Draft, int) (linkmodel.Link, error) { return linkmodel.Link{}, tt.err },
					deleteFn: func(int64, int) error { return tt.err },
				}
				h := newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler()
				rec := sendJSON(h, ep.method, ep.target, body)

				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
				}
				if got := errorCode(t, rec); got != tt.wantCode {
					t.Errorf("code = %q, want %q", got, tt.wantCode)
				}
				wantDetails := tt.wantDetails
				if tt.wantCode == "link.invalid" && ep.method != http.MethodPatch {
					// Without a draft there is no text to convert offsets
					// against; such an error cannot come from a read or a
					// delete, and the check here is only that it is not a 500.
					wantDetails = ""
				}
				if wantDetails != "" {
					if got, want := decodeJSON(t, errorDetails(t, rec)), decodeJSON(t, wantDetails); !reflect.DeepEqual(got, want) {
						t.Errorf("details = %s\nwant %s", errorDetails(t, rec), wantDetails)
					}
				}
				if strings.Contains(rec.Body.String(), "secret") {
					t.Errorf("an internal error leaked into the response: %s", rec.Body.String())
				}
				if wantLogged := tt.wantStatus == 500; strings.Contains(logs.String(), "secret") != wantLogged {
					t.Errorf("internal error logged = %v, want %v:\n%s", !wantLogged, wantLogged, logs.String())
				}
				assertNoCORS(t, rec.Header())
			})
		}
	}
}

// DIR-4 on create: the 409 says which link has the name and whether the
// caller may edit it.
func TestCreateLinkNameTakenNamesTheExistingLink(t *testing.T) {
	svc := &fakeService{createFn: func(app.Draft) (linkmodel.Link, error) {
		return linkmodel.Link{}, &app.NameTakenError{Existing: linkmodel.Link{ID: 4, Name: "reports"}, Editable: true}
	}}
	rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links", validBody)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "link.name_taken" {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := decodeJSON(t, errorDetails(t, rec)), decodeJSON(t, `{"existing":{"id":4,"name":"reports"},"canEdit":true}`); !reflect.DeepEqual(got, want) {
		t.Errorf("details = %s", errorDetails(t, rec))
	}
}

// The write guards of plan section 3.8 on PATCH and DELETE.
func TestGuardsOnUpdateAndDelete(t *testing.T) {
	const patchBody = `{"description":"","routes":[{"pattern":"gh","destination":"https://github.com/acme"}],"version":1}`
	type headers = map[string]string
	tests := []struct {
		name       string
		method     string
		headers    headers
		wantStatus int
		wantCode   string
	}{
		// PATCH: exactly as POST.
		{"PATCH from curl", "PATCH", headers{"Content-Type": "application/json"}, 200, ""},
		{"PATCH from our own page", "PATCH", headers{"Content-Type": "application/json", "Origin": "http://localhost:4655", "Sec-Fetch-Site": "same-origin"}, 200, ""},
		{"PATCH with a charset", "PATCH", headers{"Content-Type": "application/json; charset=utf-8"}, 200, ""},
		{"PATCH without a content type", "PATCH", headers{}, 415, codeUnsupportedMediaType},
		{"PATCH as plain text", "PATCH", headers{"Content-Type": "text/plain"}, 415, codeUnsupportedMediaType},
		{"PATCH as a form", "PATCH", headers{"Content-Type": "application/x-www-form-urlencoded"}, 415, codeUnsupportedMediaType},
		{"PATCH from another site", "PATCH", headers{"Content-Type": "application/json", "Origin": "https://evil.example"}, 403, codeCrossSite},
		{"PATCH from another port", "PATCH", headers{"Content-Type": "application/json", "Origin": "http://localhost:3000"}, 403, codeCrossSite},
		{"PATCH from an opaque origin", "PATCH", headers{"Content-Type": "application/json", "Origin": "null"}, 403, codeCrossSite},
		{"PATCH cross-site by fetch metadata", "PATCH", headers{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}, 403, codeCrossSite},
		{"PATCH same-site by fetch metadata", "PATCH", headers{"Content-Type": "application/json", "Sec-Fetch-Site": "same-site"}, 403, codeCrossSite},
		{"PATCH cross-site, plain text", "PATCH", headers{"Content-Type": "text/plain", "Origin": "https://evil.example"}, 403, codeCrossSite},

		// DELETE: no body, so no content type is asked for; the origin
		// checks are the same.
		{"DELETE from curl", "DELETE", headers{}, 204, ""},
		{"DELETE from our own page", "DELETE", headers{"Origin": "http://localhost:4655", "Sec-Fetch-Site": "same-origin"}, 204, ""},
		{"DELETE with a JSON content type", "DELETE", headers{"Content-Type": "application/json"}, 204, ""},
		{"DELETE with another content type", "DELETE", headers{"Content-Type": "text/plain"}, 204, ""},
		{"DELETE from another site", "DELETE", headers{"Origin": "https://evil.example"}, 403, codeCrossSite},
		{"DELETE from another port", "DELETE", headers{"Origin": "http://localhost:3000"}, 403, codeCrossSite},
		{"DELETE from an opaque origin", "DELETE", headers{"Origin": "null"}, 403, codeCrossSite},
		{"DELETE with an empty origin", "DELETE", headers{"Origin": ""}, 403, codeCrossSite},
		{"DELETE cross-site by fetch metadata", "DELETE", headers{"Sec-Fetch-Site": "cross-site"}, 403, codeCrossSite},
		{"DELETE same-site by fetch metadata", "DELETE", headers{"Sec-Fetch-Site": "same-site"}, 403, codeCrossSite},
		{"DELETE cross-site wins over own origin", "DELETE", headers{"Origin": "http://localhost:4655", "Sec-Fetch-Site": "cross-site"}, 403, codeCrossSite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			body := ""
			if tt.method == http.MethodPatch {
				body = patchBody
			}
			rec := request{method: tt.method, target: "/-/api/v1/links/12", headers: tt.headers, body: body}.send(newTestServer(svc, nil).Handler())

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode != "" {
				if got := errorCode(t, rec); got != tt.wantCode {
					t.Errorf("code = %q, want %q", got, tt.wantCode)
				}
			}
			if wantCalls := map[bool]int{true: 1, false: 0}[tt.wantStatus < 300]; svc.calls() != wantCalls {
				t.Errorf("the Service was called %d times, want %d", svc.calls(), wantCalls)
			}
			assertNoCORS(t, rec.Header())
		})
	}
}

// A header sent twice is refused whatever its copies say, on the new write
// methods as on POST.
func TestGuardsOnUpdateAndDeleteJudgeRepeatedHeaders(t *testing.T) {
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		for _, name := range []string{"Origin", "Sec-Fetch-Site"} {
			t.Run(method+" "+name, func(t *testing.T) {
				svc := &fakeService{}
				req := httptest.NewRequest(method, "/-/api/v1/links/12", strings.NewReader(`{}`))
				req.Host = testHost
				req.Header.Set("Content-Type", "application/json")
				own := map[string]string{"Origin": "http://localhost:4655", "Sec-Fetch-Site": "same-origin"}[name]
				req.Header.Add(name, own)
				req.Header.Add(name, own)
				rec := httptest.NewRecorder()
				newTestServer(svc, nil).Handler().ServeHTTP(rec, req)

				if rec.Code != http.StatusForbidden || svc.calls() != 0 {
					t.Errorf("status = %d, calls = %d; want 403 and none", rec.Code, svc.calls())
				}
			})
		}
	}
}

func TestMethodNotAllowedOnNewEndpoints(t *testing.T) {
	tests := []struct {
		method, target, wantAllow string
	}{
		{http.MethodPost, "/-/api/v1/links/12", "DELETE, GET, HEAD, PATCH"},
		{http.MethodPut, "/-/api/v1/links/12", "DELETE, GET, HEAD, PATCH"},
		{http.MethodOptions, "/-/api/v1/links/12", "DELETE, GET, HEAD, PATCH"},
		{http.MethodGet, "/-/api/v1/links/validate", "POST"},
		{http.MethodHead, "/-/api/v1/links/validate", "POST"},
		{http.MethodPatch, "/-/api/v1/links/validate", "POST"},
		{http.MethodDelete, "/-/api/v1/links/validate", "POST"},
		{http.MethodOptions, "/-/api/v1/links/validate", "POST"},
		{http.MethodPost, "/-/api/v1/resolve?path=roadmap", "GET, HEAD"},
		{http.MethodPatch, "/-/api/v1/resolve?path=roadmap", "GET, HEAD"},
		{http.MethodDelete, "/-/api/v1/resolve?path=roadmap", "GET, HEAD"},
		{http.MethodOptions, "/-/api/v1/resolve?path=roadmap", "GET, HEAD"},
		{http.MethodPatch, "/-/api/v1/links", "GET, HEAD, POST"},
		{http.MethodDelete, "/-/api/v1/links", "GET, HEAD, POST"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			svc := &fakeService{}
			rec := sendJSON(newTestServer(svc, nil).Handler(), tt.method, tt.target, `{}`)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405; body: %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Allow"); got != tt.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tt.wantAllow)
			}
			if tt.method != http.MethodHead {
				if got := errorCode(t, rec); got != codeMethodNotAllowed {
					t.Errorf("code = %q, want %q", got, codeMethodNotAllowed)
				}
			}
			if svc.calls() != 0 {
				t.Errorf("a refused method reached the Service")
			}
			assertNoCORS(t, rec.Header())
		})
	}
}

// Addresses near the new endpoints that are not endpoints.
func TestNewEndpointRouting(t *testing.T) {
	for _, target := range []string{
		"/-/api/v1/links/", "/-/api/v1/links/12/", "/-/api/v1/links/12/revisions", "/-/api/v1/links/validate/",
		"/-/api/v1/links/validate/x", "/-/api/v1/resolve/", "/-/api/v1/resolve/roadmap", "/-/api/v1/Resolve", "/-/api/v2/resolve",
	} {
		t.Run(target, func(t *testing.T) {
			svc := &fakeService{}
			rec := get(newTestServer(svc, nil).Handler(), target)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if got := errorCode(t, rec); got != codeNotFound {
				t.Errorf("code = %q, want %q", got, codeNotFound)
			}
			if svc.calls() != 0 {
				t.Errorf("the Service was called")
			}
		})
	}
}

// No response of a new endpoint carries a CORS header: not a success, not an
// error, not the answer to a preflight. That absence is what keeps a page of
// another site from reading link data.
func TestNewEndpointsNeverSendCORS(t *testing.T) {
	crossSite := map[string]string{
		"Origin":                         "https://other.example",
		"Sec-Fetch-Site":                 "cross-site",
		"Sec-Fetch-Mode":                 "cors",
		"Access-Control-Request-Method":  "PATCH",
		"Access-Control-Request-Headers": "content-type",
		"Content-Type":                   "application/json",
	}
	sameOrigin := map[string]string{
		"Origin": "http://localhost:4655", "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json",
	}
	targets := []string{
		"/-/api/v1/links/12", "/-/api/v1/links/abc", "/-/api/v1/links/validate",
		"/-/api/v1/resolve?path=roadmap", "/-/api/v1/resolve", "/-/api/v1/resolve?path=%zz",
	}
	methods := []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut}
	failing := errors.New("boom")
	services := map[string]func() *fakeService{
		"working": func() *fakeService { return &fakeService{} },
		"failing": func() *fakeService {
			return &fakeService{
				getFn:     func(int64) (linkmodel.Link, error) { return linkmodel.Link{}, app.ErrNotFound },
				updateFn:  func(int64, app.Draft, int) (linkmodel.Link, error) { return linkmodel.Link{}, &app.ValidationError{} },
				deleteFn:  func(int64, int) error { return failing },
				resolveFn: func(string) app.Decision { return app.Decision{Kind: app.DecisionInternalError} },
			}
		},
	}
	statuses := map[int]bool{}
	for name, newSvc := range services {
		for _, target := range targets {
			for _, method := range methods {
				for from, headers := range map[string]map[string]string{"cross-site": crossSite, "same-origin": sameOrigin} {
					rec := request{method: method, target: target, headers: headers,
						body: `{"description":"","routes":[],"version":1}`}.send(newTestServer(newSvc(), nil).Handler())
					statuses[rec.Code] = true
					for header := range rec.Header() {
						if strings.HasPrefix(header, "Access-Control-") {
							t.Errorf("%s service, %s %s from %s (status %d) carries %s", name, method, target, from, rec.Code, header)
						}
					}
				}
			}
		}
	}
	// The sweep is only worth something if it met successes and each kind
	// of failure.
	for _, status := range []int{200, 204, 400, 403, 404, 405, 422, 500} {
		if !statuses[status] {
			t.Errorf("the sweep never saw a %d", status)
		}
	}
}

// PRD 5.7, save example 5: a user who cannot edit a link is told whose it is
// and which link it is about, in the message itself, on PATCH and on DELETE.
func TestForbiddenMessageNamesTheOwnerAndTheLink(t *testing.T) {
	gh := linkmodel.Link{ID: 7, Name: "gh", OwnerID: 2}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"the owner is known", &app.ForbiddenError{Link: gh, Owner: "Ada Lovelace"},
			"You can't change go/gh: it belongs to Ada Lovelace. Ask them to change it, or to hand it over."},
		{"the owner cannot be named", &app.ForbiddenError{Link: gh},
			"You can't change go/gh. Only its owner or an administrator can."},
		{"not about a link", app.ErrForbidden, "You are not allowed to do this."},
	}
	for _, tt := range tests {
		for _, ep := range []struct{ method, target, body string }{
			{http.MethodPatch, "/-/api/v1/links/7", `{"description":"","routes":[{"pattern":"gh","destination":"https://example.com"}],"version":1}`},
			{http.MethodDelete, "/-/api/v1/links/7", ""},
		} {
			t.Run(tt.name+" "+ep.method, func(t *testing.T) {
				svc := &fakeService{
					updateFn: func(int64, app.Draft, int) (linkmodel.Link, error) { return linkmodel.Link{}, tt.err },
					deleteFn: func(int64, int) error { return tt.err },
				}
				rec := sendJSON(newTestServer(svc, nil).Handler(), ep.method, ep.target, ep.body)
				var doc struct {
					Error struct {
						Code, Message string
					}
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
					t.Fatalf("body %q: %v", rec.Body.String(), err)
				}
				if rec.Code != http.StatusForbidden || doc.Error.Code != "link.forbidden" || doc.Error.Message != tt.want {
					t.Errorf("%d %s %q\nwant 403 link.forbidden %q", rec.Code, doc.Error.Code, doc.Error.Message, tt.want)
				}
			})
		}
	}
}

// Without a query, or with a blank one, GET /links is the plain list it was
// before search: every link, the same JSON, and Search is not asked.
func TestListLinksWithoutQuery(t *testing.T) {
	for _, target := range []string{
		"/-/api/v1/links", "/-/api/v1/links?q=", "/-/api/v1/links?q=%20%09%20", "/-/api/v1/links?limit=3",
	} {
		t.Run(target, func(t *testing.T) {
			svc := &fakeService{links: []linkmodel.Link{sampleLink()}}
			rec := get(newTestServer(svc, nil).Handler(), target)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
			}
			assertJSON(t, rec, `{"links":[`+sampleLinkJSON+`]}`)
			if got := svc.searches(); len(got) != 0 {
				t.Errorf("Search was asked: %v", got)
			}
		})
	}
}

// With a query, GET /links reports what Search found, in its order, each
// link as every endpoint shows it plus its rank.
func TestListLinksSearch(t *testing.T) {
	gh := variableLink()
	svc := &fakeService{
		fakeQueries: fakeQueries{searchFn: func(string, int) ([]app.SearchResult, error) {
			return []app.SearchResult{
				{Link: sampleLink(), Rank: app.RankDescription},
				{Link: gh, Rank: app.RankMisspelled, Edits: 1},
			}, nil
		}},
		// Each link's canEdit is the Service's answer for that link.
		canEditFn: func(_ app.Actor, l linkmodel.Link) bool { return l.Name == "roadmap" },
	}
	rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/links?q=%20Daily+report%20&limit=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	// Each result is the link JSON plus its rank, and each of its routes
	// also carries the tokens of its pattern (in the validate endpoint's
	// shape), so results that arrive as the user types can highlight
	// variables.
	withRank := func(doc, rank string, canEdit bool, tokens ...string) string {
		var link map[string]any
		if err := json.Unmarshal([]byte(doc), &link); err != nil {
			t.Fatal(err)
		}
		link["rank"] = rank
		link["canEdit"] = canEdit
		routes := link["routes"].([]any)
		if len(routes) != len(tokens) {
			t.Fatalf("%d routes, %d token lists", len(routes), len(tokens))
		}
		for i, rt := range routes {
			var list any
			if err := json.Unmarshal([]byte(tokens[i]), &list); err != nil {
				t.Fatal(err)
			}
			rt.(map[string]any)["tokens"] = list
		}
		out, err := json.Marshal(link)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	assertJSON(t, rec, `{"links":[`+
		withRank(sampleLinkJSON, "description", true,
			`[{"kind":"literal","start":0,"end":7}]`,
			`[{"kind":"literal","start":0,"end":7},{"kind":"literal","start":8,"end":12}]`)+`,`+
		withRank(variableLinkJSON, "misspelled", false,
			`[{"kind":"literal","start":0,"end":2},{"kind":"variable","start":3,"end":9,"name":"repo","type":"string"},`+
				`{"kind":"literal","start":10,"end":12},{"kind":"variable","start":13,"end":22,"name":"num","type":"int"}]`,
			`[{"kind":"literal","start":0,"end":2},{"kind":"literal","start":3,"end":8},`+
				`{"kind":"variable","start":9,"end":42,"name":"month","type":"regex"},{"kind":"variable","start":43,"end":57,"name":"ratio","type":"number"}]`,
			`[{"kind":"literal","start":0,"end":2}]`)+`]}`)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	// The query reaches the Service trimmed, and the limit as sent; the
	// Service applies the default and the maximum.
	if got, want := svc.searches(), []searchCall{{"Daily report", 7}}; !reflect.DeepEqual(got, want) {
		t.Errorf("searches = %v, want %v", got, want)
	}
	if len(svc.actors) == 0 || svc.actors[0] != testActor {
		t.Errorf("actor = %v, want %v", svc.actors, testActor)
	}

	// Without a limit the Service hears 0, its default.
	svc.searched = nil
	get(newTestServer(svc, nil).Handler(), "/-/api/v1/links?q=gh")
	if got, want := svc.searches(), []searchCall{{"gh", 0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("searches = %v, want %v", got, want)
	}
}

func TestListLinksSearchFindsNothing(t *testing.T) {
	rec := get(newTestServer(&fakeService{}, nil).Handler(), "/-/api/v1/links?q=nothing")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	assertJSON(t, rec, `{"links":[]}`)
}

// A limit that is not a whole number of 1 or more, or a query string that
// cannot be read, is the client's mistake: 400, and nothing is asked.
func TestListLinksBadQuery(t *testing.T) {
	for _, query := range []string{
		"q=a&limit=0", "q=a&limit=-1", "q=a&limit=abc", "q=a&limit=1.5", "q=a&limit=", "q=a&limit=1&limit=2",
		"q=a&limit=99999999999999999999", "q=%zz", "limit=abc",
	} {
		t.Run(query, func(t *testing.T) {
			svc := &fakeService{}
			rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/links?"+query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
			if got := errorCode(t, rec); got != codeMalformed {
				t.Errorf("code = %q, want %q", got, codeMalformed)
			}
			if svc.calls() != 0 || len(svc.searches()) != 0 {
				t.Errorf("the Service was asked")
			}
		})
	}
}

// A failing search is a 500 that says nothing about why.
func TestListLinksSearchFails(t *testing.T) {
	svc := &fakeService{fakeQueries: fakeQueries{searchFn: func(string, int) ([]app.SearchResult, error) {
		return nil, errors.New("disk on fire")
	}}}
	rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/links?q=x")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "fire") {
		t.Errorf("status = %d, body = %s; want a bare 500", rec.Code, rec.Body.String())
	}
}
