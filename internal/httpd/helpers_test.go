package httpd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

const (
	testAddr    = "localhost:4655"
	testHost    = "localhost:4655"
	testVersion = "9.9.9-test"
)

var testActor = app.Actor{UserID: 1, DisplayName: "Local user", Admin: true}

// The tests run against a fake, so nothing else in this package would notice
// if the real use cases stopped fitting the Service interface.
var _ Service = (*app.App)(nil)

// fakeService stands in for package app. It records every call, so tests can
// assert not only what the server answered but also what it asked.
type fakeService struct {
	mu sync.Mutex

	resolveFn func(escapedPath string) app.Decision
	createFn  func(d app.Draft) (linkmodel.Link, error)
	links     []linkmodel.Link
	listErr   error
	owner     string
	canEditFn func(actor app.Actor, l linkmodel.Link) bool

	getFn      func(id int64) (linkmodel.Link, error)
	updateFn   func(id int64, d app.Draft, version int) (linkmodel.Link, error)
	deleteFn   func(id int64, version int) error
	validateFn func(req app.ValidateRequest) app.ValidateResult

	resolved  []string
	created   []app.Draft
	actors    []app.Actor
	got       []int64
	updated   []updateCall
	deleted   []deleteCall
	validated []app.ValidateRequest
}

type updateCall struct {
	id      int64
	draft   app.Draft
	version int
}

type deleteCall struct {
	id      int64
	version int
}

// calls counts every call that reads or changes links, for asserting that a
// refused request never reached the Service.
func (f *fakeService) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.resolved) + len(f.created) + len(f.got) + len(f.updated) + len(f.deleted) + len(f.validated)
}

func (f *fakeService) GetLink(_ context.Context, actor app.Actor, id int64) (linkmodel.Link, error) {
	f.mu.Lock()
	f.got = append(f.got, id)
	f.actors = append(f.actors, actor)
	fn := f.getFn
	f.mu.Unlock()
	if fn == nil {
		return sampleLink(), nil
	}
	return fn(id)
}

func (f *fakeService) UpdateLink(_ context.Context, actor app.Actor, id int64, d app.Draft, version int) (linkmodel.Link, error) {
	f.mu.Lock()
	f.updated = append(f.updated, updateCall{id, d, version})
	f.actors = append(f.actors, actor)
	fn := f.updateFn
	f.mu.Unlock()
	if fn == nil {
		return sampleLink(), nil
	}
	return fn(id, d, version)
}

func (f *fakeService) DeleteLink(_ context.Context, actor app.Actor, id int64, version int) error {
	f.mu.Lock()
	f.deleted = append(f.deleted, deleteCall{id, version})
	f.actors = append(f.actors, actor)
	fn := f.deleteFn
	f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(id, version)
}

func (f *fakeService) Validate(_ context.Context, actor app.Actor, req app.ValidateRequest) app.ValidateResult {
	f.mu.Lock()
	f.validated = append(f.validated, req)
	f.actors = append(f.actors, actor)
	fn := f.validateFn
	f.mu.Unlock()
	if fn == nil {
		return app.ValidateResult{Valid: true}
	}
	return fn(req)
}

func (f *fakeService) Resolve(escapedPath string) app.Decision {
	f.mu.Lock()
	f.resolved = append(f.resolved, escapedPath)
	fn := f.resolveFn
	f.mu.Unlock()
	if fn == nil {
		return app.Decision{Kind: app.DecisionMissUnknown, LinkName: "fake"}
	}
	return fn(escapedPath)
}

func (f *fakeService) CreateLink(_ context.Context, actor app.Actor, d app.Draft) (linkmodel.Link, error) {
	f.mu.Lock()
	f.created = append(f.created, d)
	f.actors = append(f.actors, actor)
	fn := f.createFn
	f.mu.Unlock()
	if fn == nil {
		return sampleLink(), nil
	}
	return fn(d)
}

func (f *fakeService) ListLinks(_ context.Context, actor app.Actor) ([]linkmodel.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actors = append(f.actors, actor)
	return f.links, f.listErr
}

func (f *fakeService) Owner(context.Context, linkmodel.Link) string {
	if f.owner != "" {
		return f.owner
	}
	return "Local user"
}

// CanEdit says yes unless the test supplies an answer: the rule belongs to
// package app, and the server's job is only to report it.
func (f *fakeService) CanEdit(actor app.Actor, l linkmodel.Link) bool {
	if f.canEditFn != nil {
		return f.canEditFn(actor, l)
	}
	return true
}

func (f *fakeService) resolvedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resolved...)
}

func (f *fakeService) createdDrafts() []app.Draft {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.Draft(nil), f.created...)
}

func sampleLink() linkmodel.Link {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return linkmodel.Link{
		ID:          12,
		Name:        "roadmap",
		Description: "Where we are going",
		OwnerID:     1,
		Version:     1,
		Routes: []linkmodel.Route{
			{ID: 31, Position: 0, Pattern: "roadmap", Destination: "https://docs.example.com/roadmap"},
			{ID: 32, Position: 1, Pattern: "roadmap/next", Destination: "https://docs.example.com/roadmap/next"},
		},
		CreatedAt: at,
		// A non-UTC time with sub-second precision: the API must still
		// answer in UTC, to the second.
		UpdatedAt: time.Date(2026, 10, 13, 11, 30, 0, 999, time.FixedZone("CEST", 2*60*60)),
	}
}

const sampleLinkJSON = `{
  "id": 12,
  "name": "roadmap",
  "description": "Where we are going",
  "owner": { "id": 1, "displayName": "Local user" },
  "version": 1,
  "canEdit": true,
  "routes": [
    { "id": 31, "pattern": "roadmap", "destination": "https://docs.example.com/roadmap", "variables": [] },
    { "id": 32, "pattern": "roadmap/next", "destination": "https://docs.example.com/roadmap/next", "variables": [] }
  ],
  "usage": { "total": 0, "last30Days": 0, "lastUsedAt": null },
  "createdAt": "2026-10-13T09:00:00Z",
  "updatedAt": "2026-10-13T09:30:00Z"
}`

const validBody = `{"description":"Where we are going","routes":[{"pattern":"roadmap","destination":"https://docs.example.com/roadmap"}]}`

// newTestServer builds a Server on the default address. edit may adjust the
// options.
func newTestServer(svc Service, edit func(*Options)) *Server {
	opts := Options{Addr: testAddr, Version: testVersion, Actor: testActor}
	if edit != nil {
		edit(&opts)
	}
	return New(svc, opts)
}

// request describes one test request. Host defaults to the server's own.
type request struct {
	method  string
	target  string
	host    string // "-" sends an empty Host
	headers map[string]string
	body    string
}

func (rq request) send(h http.Handler) *httptest.ResponseRecorder {
	method := rq.method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if rq.body != "" {
		body = strings.NewReader(rq.body)
	}
	req := httptest.NewRequest(method, rq.target, body)
	switch rq.host {
	case "":
		req.Host = testHost
	case "-":
		req.Host = ""
	default:
		req.Host = rq.host
	}
	for name, value := range rq.headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	return request{target: target}.send(h)
}

func postJSON(h http.Handler, target, body string) *httptest.ResponseRecorder {
	return sendJSON(h, http.MethodPost, target, body)
}

func sendJSON(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	return request{
		method:  method,
		target:  target,
		headers: map[string]string{"Content-Type": "application/json"},
		body:    body,
	}.send(h)
}

// decodeJSON decodes a JSON document into generic values, for comparing
// whole documents including every field name.
func decodeJSON(t *testing.T, doc string) any {
	t.Helper()
	var v any
	dec := json.NewDecoder(strings.NewReader(doc))
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, doc)
	}
	return v
}

// assertJSON fails unless the body is exactly the wanted document: the same
// keys, no more and no fewer, with the same values.
func assertJSON(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got, want := decodeJSON(t, rec.Body.String()), decodeJSON(t, want); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %s\nwant %s", rec.Body.String(), want)
	}
}

// errorDetails returns error.details of an API error body as a document.
func errorDetails(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var doc struct {
		Error struct {
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("not an API error: %v\n%s", err, rec.Body.String())
	}
	return string(doc.Error.Details)
}

// errorCode extracts error.code from an API error body and checks the shape:
// exactly code, message and details, with details an object.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("error Content-Type = %q, want application/json", got)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("error body is not the API error shape: %v\n%s", err, rec.Body.String())
	}
	e, ok := doc["error"]
	if !ok || len(doc) != 1 {
		t.Fatalf("error body must have exactly the key \"error\": %s", rec.Body.String())
	}
	if len(e) != 3 {
		t.Errorf("error must have exactly code, message, details: %s", rec.Body.String())
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Errorf("error has no message: %s", rec.Body.String())
	}
	if _, ok := e["details"].(map[string]any); !ok {
		t.Errorf("error.details must be an object: %s", rec.Body.String())
	}
	code, _ := e["code"].(string)
	return code
}

// assertNoCORS fails if a response carries any CORS header.
func assertNoCORS(t *testing.T, h http.Header) {
	t.Helper()
	for name := range h {
		if strings.HasPrefix(name, "Access-Control-") {
			t.Errorf("response carries the CORS header %s: %q", name, h.Get(name))
		}
	}
}
