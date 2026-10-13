package httpd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/godir/internal/app"
	"github.com/tfrydrychewicz/godir/internal/linkmodel"
)

func TestMeta(t *testing.T) {
	rec := get(newTestServer(&fakeService{}, nil).Handler(), "/-/api/v1/meta")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	want := decodeJSON(t, `{"product":"godir","version":"9.9.9-test","api":1}`)
	if got := decodeJSON(t, rec.Body.String()); !reflect.DeepEqual(got, want) {
		t.Errorf("meta = %s", rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	h := newTestServer(&fakeService{}, nil).Handler()

	rec := get(h, "/-/healthz")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("GET: status %d, body %q; want 200 \"ok\"", rec.Code, rec.Body.String())
	}
	head := request{method: http.MethodHead, target: "/-/healthz"}.send(h)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, body %q; want 200 and no body", head.Code, head.Body.String())
	}
}

func TestCreateLink(t *testing.T) {
	svc := &fakeService{}
	h := newTestServer(svc, nil).Handler()

	rec := postJSON(h, "/-/api/v1/links", `{
		"description": "Where we are going",
		"routes": [
			{"pattern": "Roadmap", "destination": "https://docs.example.com/roadmap"},
			{"pattern": "roadmap/next", "destination": "https://docs.example.com/roadmap/next"}
		]
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	// Comparing whole documents checks every field name, at every level,
	// and that there is no field the plan does not list.
	if got, want := decodeJSON(t, rec.Body.String()), decodeJSON(t, sampleLinkJSON); !reflect.DeepEqual(got, want) {
		t.Errorf("link JSON differs from plan section 3.5\n got: %s\nwant: %s", rec.Body.String(), sampleLinkJSON)
	}

	// The draft reaches the service as written: validation and lowercasing
	// are the service's business.
	wantDraft := app.Draft{
		Description: "Where we are going",
		Routes: []app.DraftRoute{
			{Pattern: "Roadmap", Destination: "https://docs.example.com/roadmap"},
			{Pattern: "roadmap/next", Destination: "https://docs.example.com/roadmap/next"},
		},
	}
	if got := svc.createdDrafts(); len(got) != 1 || !reflect.DeepEqual(got[0], wantDraft) {
		t.Errorf("draft = %+v, want %+v", got, wantDraft)
	}
	if len(svc.actors) != 1 || svc.actors[0] != testActor {
		t.Errorf("actor = %+v, want %+v", svc.actors, testActor)
	}
}

// The key order of the link JSON is part of how readable curl output is, and
// a cheap extra check on the field names.
func TestLinkJSONFieldOrder(t *testing.T) {
	rec := postJSON(newTestServer(&fakeService{}, nil).Handler(), "/-/api/v1/links", validBody)
	body := rec.Body.String()
	last := -1
	for _, key := range []string{`"id"`, `"name"`, `"description"`, `"owner"`, `"displayName"`, `"version"`, `"canEdit"`,
		`"routes"`, `"pattern"`, `"destination"`, `"variables"`, `"usage"`, `"total"`, `"last30Days"`, `"lastUsedAt"`,
		`"createdAt"`, `"updatedAt"`} {
		at := strings.Index(body, key)
		if at < 0 {
			t.Fatalf("no %s in %s", key, body)
		}
		if at < last {
			t.Errorf("%s is out of order in %s", key, body)
		}
		last = at
	}
}

// threeRouteBody is a request with three routes. The first pattern has a
// non-ASCII letter before its variable, so byte offsets and the UTF-16
// offsets the API reports differ: "żó/{repo}" has the brace at byte 5 and at
// UTF-16 index 3.
const threeRouteBody = `{"routes":[
	{"pattern":"żó/{repo}","destination":"https://example.com/1"},
	{"pattern":"gh/new","destination":"ftp://example.com/2"},
	{"pattern":"gh/old","destination":"https://example.com/3"}]}`

func TestCreateLinkErrors(t *testing.T) {
	problems := []linkmodel.Problem{
		{Severity: "error", Field: "pattern", Code: "pattern.variables_unsupported", Message: "Variables are not supported yet.", Start: 5, End: 11},
		{Severity: "warning", Field: "destination", Code: "destination.example", Message: "Just a warning."},
	}
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantDetails string
	}{
		{
			// Problems arrive grouped as the validate endpoint groups them
			// (plan section 3.5): one entry per route of the request, and
			// the ones that belong to no route beside them.
			name:       "validation",
			err:        &app.ValidationError{Routes: [][]linkmodel.Problem{problems}},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   codeLinkInvalid,
			wantDetails: `{"problems":[],"routes":[{"problems":[
				{"severity":"error","field":"pattern","code":"pattern.variables_unsupported","message":"Variables are not supported yet.","start":3,"end":9},
				{"severity":"warning","field":"destination","code":"destination.example","message":"Just a warning.","start":0,"end":0}]}]}`,
		},
		{
			name: "validation, several routes",
			err: &app.ValidationError{Routes: [][]linkmodel.Problem{
				nil,
				{{Severity: "error", Field: "destination", Code: "destination.scheme", Message: "Second route.", Start: 0, End: 3}},
				{},
			}},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   codeLinkInvalid,
			wantDetails: `{"problems":[],"routes":[
				{"problems":[]},
				{"problems":[{"severity":"error","field":"destination","code":"destination.scheme","message":"Second route.","start":0,"end":3}]},
				{"problems":[]}]}`,
		},
		{
			name: "validation, link-level",
			err: &app.ValidationError{Problems: []linkmodel.Problem{
				{Severity: "error", Field: "pattern", Code: "pattern.no_routes", Message: "A link needs at least one route."}}},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   codeLinkInvalid,
			wantDetails: `{"problems":[{"severity":"error","field":"pattern","code":"pattern.no_routes","message":"A link needs at least one route.","start":0,"end":0}],
				"routes":[]}`,
		},
		{
			name:        "validation, wrapped",
			err:         fmt.Errorf("create: %w", &app.ValidationError{}),
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    codeLinkInvalid,
			wantDetails: `{"problems":[],"routes":[]}`,
		},
		{"name taken", app.ErrNameTaken, http.StatusConflict, "link.name_taken", `{}`},
		{"name taken, wrapped", fmt.Errorf("create: %w", app.ErrNameTaken), http.StatusConflict, "link.name_taken", `{}`},
		{"forbidden", app.ErrForbidden, http.StatusForbidden, codeForbidden, `{}`},
		{"anything else", errors.New("disk I/O error: /secret/path/godir.db"), http.StatusInternalServerError, codeInternal, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{createFn: func(app.Draft) (linkmodel.Link, error) { return linkmodel.Link{}, tt.err }}
			rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links", threeRouteBody)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			var doc struct {
				Error struct {
					Details json.RawMessage `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if got, want := decodeJSON(t, string(doc.Error.Details)), decodeJSON(t, tt.wantDetails); !reflect.DeepEqual(got, want) {
				t.Errorf("details = %s, want %s", doc.Error.Details, tt.wantDetails)
			}
			if strings.Contains(rec.Body.String(), "secret") {
				t.Errorf("an internal error leaked into the response: %s", rec.Body.String())
			}
		})
	}
}

func TestCreateLinkRejectsBadBodies(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
		wantInMsg  string
	}{
		{"empty", ``, http.StatusBadRequest, codeMalformed, ""},
		{"not JSON", `pattern=roadmap`, http.StatusBadRequest, codeMalformed, ""},
		{"truncated", `{"description":"x"`, http.StatusBadRequest, codeMalformed, ""},
		{"array", `[]`, http.StatusBadRequest, codeMalformed, ""},
		{"string", `"roadmap"`, http.StatusBadRequest, codeMalformed, ""},
		{"wrong type", `{"routes":"roadmap"}`, http.StatusBadRequest, codeMalformed, "routes"},
		{"wrong nested type", `{"routes":[{"pattern":1}]}`, http.StatusBadRequest, codeMalformed, "pattern"},
		{"unknown field", `{"name":"roadmap","routes":[]}`, http.StatusBadRequest, codeMalformed, `unknown field "name"`},
		{"unknown nested field", `{"routes":[{"pattern":"x","destination":"https://x.example","id":3}]}`, http.StatusBadRequest, codeMalformed, `unknown field "id"`},
		{"misspelled field", `{"desciption":"x","routes":[]}`, http.StatusBadRequest, codeMalformed, `unknown field "desciption"`},
		{"second value", validBody + validBody, http.StatusBadRequest, codeMalformed, "extra data"},
		{"trailing garbage", validBody + ` trailing`, http.StatusBadRequest, codeMalformed, ""},
		{"trailing bracket", validBody + `]`, http.StatusBadRequest, codeMalformed, ""},
		{"too large", `{"description":"` + strings.Repeat("x", maxBodyBytes) + `","routes":[]}`, http.StatusRequestEntityTooLarge, codeTooLarge, ""},
		{"too large after a valid value", validBody + strings.Repeat(" ", maxBodyBytes), http.StatusRequestEntityTooLarge, codeTooLarge, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links", tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), strings.ReplaceAll(tt.wantInMsg, `"`, `\"`)) {
				t.Errorf("message does not mention %q: %s", tt.wantInMsg, rec.Body.String())
			}
			if len(svc.createdDrafts()) != 0 {
				t.Errorf("a bad body reached the service")
			}
		})
	}
}

// Bodies the HTTP layer accepts are the service's to judge, not httpd's.
func TestCreateLinkLeavesValidationToTheService(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"routes":[]}`, `{"routes":[{}]}`, validBody + "\n \n"} {
		svc := &fakeService{}
		rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links", body)
		if rec.Code != http.StatusCreated || len(svc.createdDrafts()) != 1 {
			t.Errorf("body %q: status %d, service calls %d; want it passed on", body, rec.Code, len(svc.createdDrafts()))
		}
	}
}

func TestListLinks(t *testing.T) {
	second := sampleLink()
	second.ID, second.Name, second.Routes = 13, "zebra", nil

	tests := []struct {
		name  string
		links []linkmodel.Link
		want  string
	}{
		{"none", nil, `{"links":[]}`},
		{"one", []linkmodel.Link{sampleLink()}, `{"links":[` + sampleLinkJSON + `]}`},
		{"a link without routes still has a routes array", []linkmodel.Link{second}, `{"links":[{
			"id": 13, "name": "zebra", "description": "Where we are going",
			"owner": {"id": 1, "displayName": "Local user"}, "version": 1, "canEdit": true,
			"routes": [], "usage": {"total": 0, "last30Days": 0, "lastUsedAt": null},
			"createdAt": "2026-10-13T09:00:00Z", "updatedAt": "2026-10-13T09:30:00Z"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{links: tt.links}
			h := newTestServer(svc, nil).Handler()

			// The query is accepted and ignored until search arrives (L3).
			rec := get(h, "/-/api/v1/links?q=road&limit=5")

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got, want := decodeJSON(t, rec.Body.String()), decodeJSON(t, tt.want); !reflect.DeepEqual(got, want) {
				t.Errorf("got  %s\nwant %s", rec.Body.String(), tt.want)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}

			head := request{method: http.MethodHead, target: "/-/api/v1/links"}.send(h)
			if head.Code != http.StatusOK || head.Body.Len() != 0 {
				t.Errorf("HEAD: status %d, body %d bytes; want 200 and no body", head.Code, head.Body.Len())
			}
			if got, want := head.Header().Get("Content-Length"), rec.Header().Get("Content-Length"); got != want {
				t.Errorf("HEAD Content-Length = %q, GET %q", got, want)
			}
		})
	}
}

func TestListLinksKeepsServiceOrder(t *testing.T) {
	a, b, c := sampleLink(), sampleLink(), sampleLink()
	a.Name, b.Name, c.Name = "zebra", "alpha", "middle"
	rec := get(newTestServer(&fakeService{links: []linkmodel.Link{a, b, c}}, nil).Handler(), "/-/api/v1/links")

	var doc struct {
		Links []struct{ Name string } `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range doc.Links {
		names = append(names, l.Name)
	}
	if want := []string{"zebra", "alpha", "middle"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want the service's order %v", names, want)
	}
}

func TestListLinksError(t *testing.T) {
	svc := &fakeService{listErr: errors.New("database is locked: /secret/path")}
	rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/links")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := errorCode(t, rec); got != codeInternal {
		t.Errorf("code = %q, want %q", got, codeInternal)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("an internal error leaked into the response: %s", rec.Body.String())
	}
}

// The server holds no permission rule of its own: canEdit is whatever the
// Service answers for the request's actor and the link being shown.
func TestOwnerAndCanEdit(t *testing.T) {
	tests := []struct {
		name    string
		actor   app.Actor
		owner   string
		canEdit bool
	}{
		{"the service allows", app.Actor{UserID: 1, Admin: true}, "Local user", true},
		{"the service refuses an admin", app.Actor{UserID: 1, Admin: true}, "Local user", false},
		{"the service allows a stranger", app.Actor{UserID: 7}, "Someone else", true},
		{"the service refuses", app.Actor{UserID: 7}, "Someone else", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type question struct {
				actor  app.Actor
				linkID int64
			}
			var asked []question
			svc := &fakeService{links: []linkmodel.Link{sampleLink()}, owner: tt.owner}
			svc.canEditFn = func(actor app.Actor, l linkmodel.Link) bool {
				asked = append(asked, question{actor, l.ID})
				return tt.canEdit
			}
			rec := get(newTestServer(svc, func(o *Options) { o.Actor = tt.actor }).Handler(), "/-/api/v1/links")

			var doc struct {
				Links []struct {
					CanEdit bool `json:"canEdit"`
					Owner   struct {
						ID          int64  `json:"id"`
						DisplayName string `json:"displayName"`
					} `json:"owner"`
				} `json:"links"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Links) != 1 {
				t.Fatalf("unexpected body (%v): %s", err, rec.Body.String())
			}
			got := doc.Links[0]
			if got.CanEdit != tt.canEdit {
				t.Errorf("canEdit = %v, want the service's answer %v", got.CanEdit, tt.canEdit)
			}
			if want := []question{{tt.actor, sampleLink().ID}}; !reflect.DeepEqual(asked, want) {
				t.Errorf("the service was asked %+v, want %+v", asked, want)
			}
			if got.Owner.ID != 1 || got.Owner.DisplayName != tt.owner {
				t.Errorf("owner = %+v, want id 1 and name %q", got.Owner, tt.owner)
			}
		})
	}
}

func TestInternalRouting(t *testing.T) {
	tests := []struct {
		name       string
		req        request
		wantStatus int
		wantCode   string // API error code; "" when the answer is not an API error
		wantAllow  string
		wantUI     bool
	}{
		{"UI root", request{target: "/-/"}, http.StatusTeapot, "", "", true},
		{"UI page", request{target: "/-/links/roadmap"}, http.StatusTeapot, "", "", true},
		{"UI page with query", request{target: "/-/new?pattern=x"}, http.StatusTeapot, "", "", true},
		{"a path that only starts like the API", request{target: "/-/apiary"}, http.StatusTeapot, "", "", true},
		{"a path that only starts like healthz", request{target: "/-/healthz/x"}, http.StatusTeapot, "", "", true},

		{"unknown API endpoint", request{target: "/-/api/v1/nothing"}, http.StatusNotFound, codeNotFound, "", false},
		{"unknown API version", request{target: "/-/api/v2/meta"}, http.StatusNotFound, codeNotFound, "", false},
		{"API root", request{target: "/-/api/"}, http.StatusNotFound, codeNotFound, "", false},
		{"links with a trailing slash", request{target: "/-/api/v1/links/"}, http.StatusNotFound, codeNotFound, "", false},
		{"a link by id (L2)", request{target: "/-/api/v1/links/12"}, http.StatusNotFound, codeNotFound, "", false},

		{"DELETE links", request{method: http.MethodDelete, target: "/-/api/v1/links", headers: map[string]string{"Content-Type": "application/json"}},
			http.StatusMethodNotAllowed, codeMethodNotAllowed, "GET, HEAD, POST", false},
		{"POST meta", request{method: http.MethodPost, target: "/-/api/v1/meta", headers: map[string]string{"Content-Type": "application/json"}},
			http.StatusMethodNotAllowed, codeMethodNotAllowed, "GET, HEAD", false},
		{"OPTIONS links", request{method: http.MethodOptions, target: "/-/api/v1/links"},
			http.StatusMethodNotAllowed, codeMethodNotAllowed, "GET, HEAD, POST", false},

		// The inner ServeMux would redirect these to a cleaned path; a
		// cleaned "/-/../x" is the go link "/x".
		{"dot-dot out of the prefix", request{target: "/-/../roadmap"}, http.StatusNotFound, codeNotFound, "", false},
		{"dot-dot inside the prefix", request{target: "/-/api/../healthz"}, http.StatusNotFound, codeNotFound, "", false},
		{"dot segment", request{target: "/-/./healthz"}, http.StatusNotFound, codeNotFound, "", false},
		{"double slash", request{target: "/-//healthz"}, http.StatusNotFound, codeNotFound, "", false},
		{"double slash later", request{target: "/-/api//v1/meta"}, http.StatusNotFound, codeNotFound, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reachedUI := false
			ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reachedUI = true
				w.WriteHeader(http.StatusTeapot)
			})
			svc := &fakeService{}
			rec := tt.req.send(newTestServer(svc, func(o *Options) { o.UI = ui }).Handler())

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if reachedUI != tt.wantUI {
				t.Errorf("reached the UI handler = %v, want %v", reachedUI, tt.wantUI)
			}
			if tt.wantCode != "" {
				if got := errorCode(t, rec); got != tt.wantCode {
					t.Errorf("code = %q, want %q", got, tt.wantCode)
				}
			}
			if got := rec.Header().Get("Allow"); got != tt.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tt.wantAllow)
			}
			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("Location = %q: nothing under /-/ may redirect", got)
			}
			if got := svc.resolvedPaths(); len(got) != 0 {
				t.Errorf("a path under /-/ reached the resolver: %v", got)
			}
		})
	}
}

func TestNilUIAnswers404(t *testing.T) {
	rec := get(newTestServer(&fakeService{}, nil).Handler(), "/-/")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Offsets on the wire are UTF-16 code units, the index a browser uses for the
// same string, whatever comes before the problem.
func TestUTF16Offset(t *testing.T) {
	tests := []struct {
		text       string
		byteOffset int
		want       int
	}{
		{"roadmap", 0, 0},
		{"roadmap", 4, 4},
		{"roadmap", 7, 7},
		{"żó/x", 0, 0},
		{"żó/x", 2, 1}, // after ż, a two-byte letter that is one unit
		{"żó/x", 4, 2},
		{"żó/x", 6, 4},
		{"日本語/x", 9, 3}, // three-byte characters, one unit each
		{"a😀b", 1, 1},
		{"a😀b", 5, 3}, // an emoji is four bytes and two units
		{"a😀b", 6, 4},
		// Out of range is clamped, never a panic.
		{"abc", 99, 3},
		{"abc", -1, 0},
		{"", 0, 0},
		// An offset inside a character counts the bytes before it as one
		// replacement character; the link model never produces such offsets.
		{"ż", 1, 1},
	}
	for _, tt := range tests {
		if got := utf16Offset(tt.text, tt.byteOffset); got != tt.want {
			t.Errorf("utf16Offset(%q, %d) = %d, want %d", tt.text, tt.byteOffset, got, tt.want)
		}
	}
}

// The whole path from the real link rules to the wire: a draft with problems
// in several routes answers 422 with each problem under its own route, and
// with offsets a browser can use on the text it sent.
func TestInvalidDraftAnswersProblemsPerRoute(t *testing.T) {
	svc := &fakeService{createFn: func(d app.Draft) (linkmodel.Link, error) {
		_, problems := linkmodel.ValidateDraft(d)
		return linkmodel.Link{}, &app.ValidationError{Problems: problems.Link, Routes: problems.Routes}
	}}
	h := newTestServer(svc, nil).Handler()

	type problem struct {
		Field, Code string
		Start, End  int
		Message     string
	}
	var doc struct {
		Error struct {
			Code    string
			Details struct {
				Problems []problem
				Routes   []struct{ Problems []problem }
			}
		}
	}
	decode := func(body string) {
		t.Helper()
		rec := postJSON(h, "/-/api/v1/links", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
		}
		doc.Error.Details.Problems, doc.Error.Details.Routes = nil, nil
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
	}

	decode(`{"routes":[
		{"pattern":"gh","destination":"https://example.com/ż ó"},
		{"pattern":"dócs/x","destination":"https://example.com/2"},
		{"pattern":"gh/new","destination":"https://example.com/3"}]}`)
	routes := doc.Error.Details.Routes
	if len(doc.Error.Details.Problems) != 0 || len(routes) != 3 {
		t.Fatalf("details = %+v, want no link-level problems and 3 route entries", doc.Error.Details)
	}
	// "https://example.com/ż ó": the space is at byte 22 and at index 21.
	if got := routes[0].Problems; len(got) != 1 || got[0].Code != linkmodel.CodeDestinationBadCharacter ||
		got[0].Field != "destination" || got[0].Start != 21 || got[0].End != 22 {
		t.Errorf("route 1 problems = %+v, want the space at [21,22)", got)
	}
	// "dócs/x": ó is bytes 1 to 3 and index 1 to 2.
	if got := routes[1].Problems; len(got) != 1 || got[0].Code != linkmodel.CodePatternBadCharacter ||
		got[0].Field != "pattern" || got[0].Start != 1 || got[0].End != 2 {
		t.Errorf("route 2 problems = %+v, want the letter at [1,2)", got)
	}
	if got := routes[2].Problems; got == nil || len(got) != 0 {
		t.Errorf("route 3 problems = %+v, want an empty list", got)
	}
	for i, route := range routes {
		for _, p := range route.Problems {
			if strings.HasPrefix(p.Message, "Route ") {
				t.Errorf("route %d: message %q names its route; its place in the list already does", i+1, p.Message)
			}
		}
	}

	decode(`{"routes":[]}`)
	if got := doc.Error.Details; len(got.Problems) != 1 || got.Problems[0].Code != linkmodel.CodeNoRoutes || got.Routes == nil || len(got.Routes) != 0 {
		t.Errorf("details for a draft without routes = %+v, want one link-level problem and an empty routes list", got)
	}
}

// A destination is case-sensitive text (document ids, tokens); nothing on the
// way out may fold, re-encode or trim it.
const verbatimDestination = "https://Docs.Example.com/Road-Map/1aB_cD?Tab=A%2Fb&X=Y#Frag"

func TestLinkJSONKeepsDestinationVerbatim(t *testing.T) {
	for _, destination := range []string{verbatimDestination, "https://example.com/Żółć?Q=Ü#Zażółć"} {
		link := sampleLink()
		link.Routes = []linkmodel.Route{{ID: 31, Pattern: "roadmap", Destination: destination}}
		svc := &fakeService{links: []linkmodel.Link{link}, createFn: func(app.Draft) (linkmodel.Link, error) { return link, nil }}
		h := newTestServer(svc, nil).Handler()

		var created struct {
			Routes []struct{ Destination string }
		}
		if err := json.Unmarshal(postJSON(h, "/-/api/v1/links", validBody).Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		var listed struct {
			Links []struct {
				Routes []struct{ Destination string }
			}
		}
		if err := json.Unmarshal(get(h, "/-/api/v1/links").Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
		if len(created.Routes) != 1 || created.Routes[0].Destination != destination {
			t.Errorf("POST returned %+v, want the destination %q", created, destination)
		}
		if len(listed.Links) != 1 || listed.Links[0].Routes[0].Destination != destination {
			t.Errorf("GET returned %+v, want the destination %q", listed, destination)
		}
	}
}

// What the client sent is what the use case receives.
func TestCreateLinkPassesTheDestinationVerbatim(t *testing.T) {
	svc := &fakeService{}
	body := `{"routes":[{"pattern":"RoadMap","destination":"` + verbatimDestination + `"}]}`
	if rec := postJSON(newTestServer(svc, nil).Handler(), "/-/api/v1/links", body); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	drafts := svc.createdDrafts()
	if len(drafts) != 1 || len(drafts[0].Routes) != 1 ||
		drafts[0].Routes[0].Destination != verbatimDestination || drafts[0].Routes[0].Pattern != "RoadMap" {
		t.Errorf("the service received %+v, want the pattern and destination exactly as sent", drafts)
	}
}
