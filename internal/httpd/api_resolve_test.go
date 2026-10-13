package httpd

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

const monthPattern = "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}"

func reportsLink() *linkmodel.Link {
	return &linkmodel.Link{
		ID: 4, Name: "reports", Description: "Daily reports", OwnerID: 1, Version: 2,
		Routes: []linkmodel.Route{
			{ID: 9, Pattern: monthPattern, Destination: "https://reports.example.com/daily?month={month}&day={day}"},
			{ID: 10, Position: 1, Pattern: "reports", Destination: "https://reports.example.com/"},
		},
	}
}

// The decisions of plan section 3.5 and PRD 5.7, as the use case would
// return them.
func hitDecision() app.Decision {
	link := reportsLink()
	return app.Decision{
		Kind: app.DecisionHit, LinkName: "reports", Link: link, Route: &link.Routes[0],
		Values:      map[string]string{"month": "10", "day": "13"},
		Destination: "https://reports.example.com/daily?month=10&day=13",
	}
}

func missKnownDecision() app.Decision {
	return app.Decision{
		Kind: app.DecisionMissKnown, LinkName: "reports", Link: reportsLink(),
		Reasons: []linkmodel.Reason{
			{RouteID: 9, Pattern: monthPattern, Code: "value_rejected", Variable: "month", Value: "13",
				Accepts: "text matching ^(0?[1-9]|1[0-2])$",
				Message: "month doesn't accept \"13\". It accepts text matching ^(0?[1-9]|1[0-2])$."},
			{RouteID: 10, Pattern: "reports", Code: "segment_count",
				Message: "This route takes nothing after reports, but the address has 2 more parts."},
		},
	}
}

func missUnknownDecision() app.Decision {
	return app.Decision{
		Kind: app.DecisionMissUnknown, LinkName: "reprots", RequestedName: "reprots",
		Suggestions: []app.Suggestion{
			{Name: "reports", Path: "reports/10/13", Edits: 1, Resolves: true, Description: "Daily reports",
				Destination: "https://reports.example.com/daily?month=10&day=13"},
			{Name: "report", Path: "report", Edits: 2, Resolves: false, Description: ""},
		},
	}
}

func TestResolveAPI(t *testing.T) {
	routeWithoutVariables := hitDecision()
	routeWithoutVariables.Route = &routeWithoutVariables.Link.Routes[1]
	routeWithoutVariables.Values = nil
	routeWithoutVariables.Destination = "https://reports.example.com/"

	tests := []struct {
		name     string
		decision app.Decision
		want     string
	}{
		{"hit", hitDecision(), `{
			"kind": "hit",
			"link": { "id": 4, "name": "reports", "description": "Daily reports" },
			"route": { "id": 9, "pattern": "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}" },
			"values": { "month": "10", "day": "13" },
			"destination": "https://reports.example.com/daily?month=10&day=13" }`},
		{"hit on a route without variables", routeWithoutVariables, `{
			"kind": "hit",
			"link": { "id": 4, "name": "reports", "description": "Daily reports" },
			"route": { "id": 10, "pattern": "reports" },
			"values": {},
			"destination": "https://reports.example.com/" }`},
		{"miss, known name", missKnownDecision(), `{
			"kind": "miss_known",
			"link": { "id": 4, "name": "reports", "description": "Daily reports" },
			"routes": [
				{ "id": 9, "pattern": "reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}",
				  "reason": { "code": "value_rejected", "variable": "month", "value": "13",
				              "accepts": "text matching ^(0?[1-9]|1[0-2])$",
				              "message": "month doesn't accept \"13\". It accepts text matching ^(0?[1-9]|1[0-2])$." } },
				{ "id": 10, "pattern": "reports",
				  "reason": { "code": "segment_count", "variable": "", "value": "", "accepts": "",
				              "message": "This route takes nothing after reports, but the address has 2 more parts." } } ] }`},
		{"miss, unknown name", missUnknownDecision(), `{
			"kind": "miss_unknown",
			"requestedName": "reprots",
			"suggestions": [
				{ "name": "reports", "path": "reports/10/13", "edits": 1, "resolves": true,
				  "description": "Daily reports",
				  "destination": "https://reports.example.com/daily?month=10&day=13" },
				{ "name": "report", "path": "report", "edits": 2, "resolves": false, "description": "", "destination": "" } ] }`},
		{"miss, unknown name, nothing close", app.Decision{Kind: app.DecisionMissUnknown, LinkName: "zzz", RequestedName: "zzz"},
			`{ "kind": "miss_unknown", "requestedName": "zzz", "suggestions": [] }`},
		{"miss, not a possible name", app.Decision{Kind: app.DecisionMissUnknown},
			`{ "kind": "miss_unknown", "requestedName": "", "suggestions": [] }`},
		{"home", app.Decision{Kind: app.DecisionHome}, `{ "kind": "home" }`},
		{"bad request", app.Decision{Kind: app.DecisionBadRequest}, `{ "kind": "bad_request" }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision { return tt.decision }}
			h := newTestServer(svc, nil).Handler()

			rec := get(h, "/-/api/v1/resolve?path=reports/10/13")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			assertJSON(t, rec, tt.want)
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := rec.Header().Get("Location"); got != "" {
				t.Errorf("the API redirected to %q", got)
			}
			assertNoCORS(t, rec.Header())

			head := request{method: http.MethodHead, target: "/-/api/v1/resolve?path=reports/10/13"}.send(h)
			if head.Code != http.StatusOK || head.Body.Len() != 0 {
				t.Errorf("HEAD status = %d with %d body bytes, want 200 and none", head.Code, head.Body.Len())
			}
			if !reflect.DeepEqual(head.Header(), rec.Header()) {
				t.Errorf("HEAD headers differ from GET")
			}
		})
	}
}

// The path parameter is decoded once, as a query value, and then handed over
// unchanged: the second layer of encoding is the go link's own and belongs to
// the link model.
func TestResolveAPIPathDecoding(t *testing.T) {
	tests := []struct {
		query    string
		wantPath string
	}{
		{"path=reports/10/13", "reports/10/13"},
		{"path=reports%2F10%2F13", "reports/10/13"},
		{"path=%2Freports%2F10", "/reports/10"},
		{"path=/reports//10/", "/reports//10/"},
		// %252F is an encoded "%2F": the go link has a slash inside a value.
		{"path=docs%2Fa%252Fb", "docs/a%2Fb"},
		{"path=docs/a%252Fb", "docs/a%2Fb"},
		// %2525 is an encoded "%25": the value is "100%".
		{"path=sale%2F100%2525", "sale/100%25"},
		// %25 alone leaves a bare "%": a malformed go link, which is the
		// resolver's to call a bad request.
		{"path=sale%2F100%25", "sale/100%"},
		{"path=a%25zz", "a%zz"},
		{"path=a%2541", "a%41"},
		// Form decoding: "+" is a space; an encoded plus stays a plus.
		{"path=a+b", "a b"},
		{"path=a%2Bb", "a+b"},
		{"path=a%20b", "a b"},
		{"path=a%2520b", "a%20b"},
		{"path=reports/../x", "reports/../x"},
		{"path=reports%3Ftab%3D1", "reports?tab=1"},
		{"path=reports%23top", "reports#top"},
		{"path=%C5%BC%C3%B3%C5%82%C4%87", "żółć"},
		{"path=%FF", "\xff"},
		{"path=%00", "\x00"},
		{"path=", ""},
		{"path", ""},
		{"path=a=b", "a=b"},
		{"other=1&path=roadmap&x=y", "roadmap"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			svc := &fakeService{}
			rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/resolve?"+tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			if got := svc.resolvedPaths(); len(got) != 1 || got[0] != tt.wantPath {
				t.Errorf("resolved %q, want [%q]", got, tt.wantPath)
			}
		})
	}
}

func TestResolveAPIRejectsMalformedRequests(t *testing.T) {
	for _, query := range []string{
		"",                  // no path at all
		"q=roadmap",         // another parameter
		"Path=roadmap",      // parameter names are case-sensitive
		"path=a&path=b",     // which one?
		"path=a&path=a",     // even when they agree
		"path=%zz",          // not a query string
		"path=%",            // a bare percent sign
		"path=a%2",          // a cut-off escape
		"path=a;b",          // semicolons are not separators
		"path=ok&other=%zz", // one bad pair spoils the whole query
	} {
		t.Run(query, func(t *testing.T) {
			svc := &fakeService{}
			target := "/-/api/v1/resolve"
			if query != "" {
				target += "?" + query
			}
			rec := get(newTestServer(svc, nil).Handler(), target)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
			if got := errorCode(t, rec); got != codeMalformed {
				t.Errorf("code = %q, want %q", got, codeMalformed)
			}
			if svc.calls() != 0 {
				t.Errorf("the resolver was asked: %q", svc.resolvedPaths())
			}
		})
	}
}

// A decision that cannot be shown answers 500 in the error shape, and the
// log names the link and nothing of the request.
func TestResolveAPIInternalError(t *testing.T) {
	hitWithoutLink := hitDecision()
	hitWithoutLink.Link = nil
	hitWithoutRoute := hitDecision()
	hitWithoutRoute.Route = nil
	missWithoutLink := missKnownDecision()
	missWithoutLink.Link = nil

	tests := map[string]app.Decision{
		"expansion failed":        {Kind: app.DecisionInternalError, LinkName: "reports", Link: reportsLink()},
		"unknown kind":            {Kind: app.DecisionKind(99), LinkName: "reports"},
		"hit without a link":      hitWithoutLink,
		"hit without a route":     hitWithoutRoute,
		"known miss without link": missWithoutLink,
	}
	for name, decision := range tests {
		t.Run(name, func(t *testing.T) {
			logger, logs := captureLog()
			svc := &fakeService{resolveFn: func(string) app.Decision { return decision }}
			rec := get(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler(),
				"/-/api/v1/resolve?path=reports/S3cr3t-Value/13")

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
			}
			if got := errorCode(t, rec); got != codeInternal {
				t.Errorf("code = %q, want %q", got, codeInternal)
			}
			if strings.Contains(rec.Body.String(), "reports.example.com") {
				t.Errorf("the response carries a destination: %s", rec.Body.String())
			}
			log := logs.String()
			if !strings.Contains(log, `"level":"ERROR"`) || !strings.Contains(log, `"link":"reports"`) {
				t.Errorf("the failure is not logged with its link:\n%s", log)
			}
			for _, secret := range []string{"S3cr3t", "reports.example.com", "month", "?", "path"} {
				if strings.Contains(log, secret) {
					t.Errorf("the log contains %q:\n%s", secret, log)
				}
			}
		})
	}
}

// The refused text of a known-name miss is repeated once per route, so the
// API bounds it; the message beside it is the link model's and is left alone.
func TestResolveAPIBoundsReasonValues(t *testing.T) {
	long := strings.Repeat("ż", 5000) // two bytes each: a cut must not split one
	d := missKnownDecision()
	d.Reasons[0].Value = long
	svc := &fakeService{resolveFn: func(string) app.Decision { return d }}
	rec := get(newTestServer(svc, nil).Handler(), "/-/api/v1/resolve?path=x")

	var doc struct {
		Routes []struct {
			Reason struct{ Value, Message string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	value := doc.Routes[0].Reason.Value
	if len(value) != maxReasonValue || value != strings.Repeat("ż", maxReasonValue/2) {
		t.Errorf("value has %d bytes, want %d whole characters", len(value), maxReasonValue/2)
	}
	if doc.Routes[0].Reason.Message != d.Reasons[0].Message {
		t.Errorf("the message was changed")
	}
	if rec.Body.Len() > 2000 {
		t.Errorf("the response has %d bytes", rec.Body.Len())
	}
}

func TestCutText(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"", 3, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"aż", 2, "a"},  // "ż" would be split
		{"aż", 3, "aż"}, // fits exactly
		{"😀", 3, ""},
		{"a😀", 4, "a"},
		{"abc", 0, ""},
	}
	for _, tt := range tests {
		if got := cutText(tt.in, tt.n); got != tt.want {
			t.Errorf("cutText(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

// The resolve endpoint does as much work as a go link does. A page of another
// site may not set that work in motion from a script, an image or a frame,
// any more than it may load a go link that way: the request is refused before
// anything is resolved. (It could never read the answer; there are no CORS
// headers. The refusal is about the work, and about telling nothing apart.)
func TestResolveAPIRefusesCrossSiteSubresourceRequests(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		served  bool
	}{
		{"a fetch from another site", map[string]string{"Origin": "https://other.example", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, false},
		{"a no-cors fetch from another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "empty"}, false},
		{"an image on another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false},
		{"a frame on another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}, false},
		{"another port of localhost", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, false},
		{"the Directory's own page", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, true},
		{"typed into the address bar", map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, true},
		{"a link clicked on another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, true},
		{"no Fetch Metadata: curl, another server", map[string]string{}, true},
		{"only an Origin: an old browser", map[string]string{"Origin": "https://other.example"}, true},
	}
	for _, tt := range tests {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(tt.name+" "+method, func(t *testing.T) {
				svc := &fakeService{resolveFn: func(string) app.Decision { return hitDecision() }}
				rec := request{method: method, target: "/-/api/v1/resolve?path=reports/10/13", headers: tt.headers}.send(newTestServer(svc, nil).Handler())
				assertNoCORS(t, rec.Header())
				if tt.served {
					if rec.Code != http.StatusOK || len(svc.resolvedPaths()) != 1 {
						t.Errorf("status = %d after %d resolutions, want 200 after 1", rec.Code, len(svc.resolvedPaths()))
					}
					return
				}
				if rec.Code != http.StatusForbidden {
					t.Errorf("status = %d, want 403", rec.Code)
				}
				if n := len(svc.resolvedPaths()); n != 0 {
					t.Errorf("a refused request was resolved %d times", n)
				}
				if method == http.MethodGet && errorCode(t, rec) != codeCrossSite {
					t.Errorf("body = %s, want the code %s", rec.Body.String(), codeCrossSite)
				}
				if strings.Contains(rec.Body.String(), "reports") {
					t.Errorf("the refusal says something about the link: %s", rec.Body.String())
				}
			})
		}
	}
}
