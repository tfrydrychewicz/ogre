package httpd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// The plain-text pages of the resolver, whole: every line is the server's
// own wording or comes from the decision.
func TestResolverPages(t *testing.T) {
	longValue := missKnownDecision()
	longValue.Link.Description = ""

	tests := []struct {
		name       string
		decision   app.Decision
		wantStatus int
		wantBody   string
	}{
		{
			name:       "unknown name with suggestions",
			decision:   missUnknownDecision(),
			wantStatus: http.StatusNotFound,
			wantBody: "404: go/reprots is not a go link yet.\n" +
				"\n" +
				"Did you mean go/reports/10/13? (Daily reports)\n" +
				"  It opens https://reports.example.com/daily?month=10&day=13\n" +
				"\n" +
				"Did you mean go/report?\n",
		},
		{
			// PRD 5.6: a suggestion that does not resolve "opens that link's
			// usage help". go/gh has a route for its bare name, so following
			// the suggestion would redirect and explain nothing: the page
			// shows the routes itself.
			name:       "a suggestion the rest of the address does not fit",
			decision:   missUnknownWithUsage(),
			wantStatus: http.StatusNotFound,
			wantBody: "404: go/ghh is not a go link yet.\n" +
				"\n" +
				"Did you mean go/gh? (GitHub)\n" +
				"  This address does not fit it as written. go/gh is used like this:\n" +
				"    go/gh\n" +
				"    go/gh/{repo}\n" +
				"    go/gh/{repo}/pr/{num:int}\n" +
				"\n" +
				"Did you mean go/gha/a/b?\n" +
				"  It opens https://example.com/actions/a/b\n",
		},
		{
			name:       "unknown name, nothing close",
			decision:   app.Decision{Kind: app.DecisionMissUnknown, LinkName: "zzz", RequestedName: "zzz"},
			wantStatus: http.StatusNotFound,
			wantBody:   "404: go/zzz is not a go link yet.\n",
		},
		{
			name:       "not a possible name",
			decision:   app.Decision{Kind: app.DecisionMissUnknown},
			wantStatus: http.StatusNotFound,
			wantBody:   "404: that is not a go link.\n",
		},
		{
			name:       "known name, a reason per route",
			decision:   missKnownDecision(),
			wantStatus: http.StatusNotFound,
			wantBody: "404: go/reports exists, but nothing in it matches this address.\n" +
				"\n" +
				"Daily reports\n" +
				"\n" +
				"go/reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}\n" +
				"  month doesn't accept \"13\". It accepts text matching ^(0?[1-9]|1[0-2])$.\n" +
				"\n" +
				"go/reports\n" +
				"  This route takes nothing after reports, but the address has 2 more parts.\n",
		},
		{
			name:       "known name without a description",
			decision:   longValue,
			wantStatus: http.StatusNotFound,
			wantBody: "404: go/reports exists, but nothing in it matches this address.\n" +
				"\n" +
				"go/reports/{month:regex(^(0?[1-9]|1[0-2])$)}/{day:int}\n" +
				"  month doesn't accept \"13\". It accepts text matching ^(0?[1-9]|1[0-2])$.\n" +
				"\n" +
				"go/reports\n" +
				"  This route takes nothing after reports, but the address has 2 more parts.\n",
		},
		{
			name:       "known name, nothing else known",
			decision:   app.Decision{Kind: app.DecisionMissKnown, LinkName: "reports"},
			wantStatus: http.StatusNotFound,
			wantBody:   "404: go/reports exists, but nothing in it matches this address.\n",
		},
		{
			name:       "bad request",
			decision:   app.Decision{Kind: app.DecisionBadRequest},
			wantStatus: http.StatusBadRequest,
			wantBody:   "400: this address is not valid text, so it cannot be a go link.\n",
		},
		{
			name:       "internal error",
			decision:   app.Decision{Kind: app.DecisionInternalError, LinkName: "reports", Link: reportsLink()},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "500 Internal Server Error\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision { return tt.decision }}
			h := newTestServer(svc, nil).Handler()

			getRec := get(h, "/Typed-Path/Typed-Value?typed=query")
			headRec := request{method: http.MethodHead, target: "/Typed-Path/Typed-Value?typed=query"}.send(h)

			for method, rec := range map[string]*httptest.ResponseRecorder{"GET": getRec, "HEAD": headRec} {
				if rec.Code != tt.wantStatus {
					t.Errorf("%s status = %d, want %d", method, rec.Code, tt.wantStatus)
				}
				if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("%s Content-Type = %q, want plain text", method, got)
				}
				if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
					t.Errorf("%s X-Content-Type-Options = %q, want nosniff", method, got)
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("%s Cache-Control = %q, want no-store", method, got)
				}
				if got := rec.Header().Get("Location"); got != "" {
					t.Errorf("%s redirects to %q", method, got)
				}
				assertNoCORS(t, rec.Header())
			}
			if got := getRec.Body.String(); got != tt.wantBody {
				t.Errorf("body:\n%s\nwant:\n%s", got, tt.wantBody)
			}
			if headRec.Body.Len() != 0 {
				t.Errorf("HEAD wrote a body: %q", headRec.Body.String())
			}
			if !reflect.DeepEqual(getRec.Header(), headRec.Header()) {
				t.Errorf("HEAD headers differ from GET:\n GET  %v\n HEAD %v", getRec.Header(), headRec.Header())
			}
			// Nothing of the request itself is on the page.
			if strings.Contains(getRec.Body.String(), "yped") {
				t.Errorf("the page repeats the request: %q", getRec.Body.String())
			}
		})
	}
}

// An expansion failure must never redirect, whatever the decision carries,
// and is logged as an error that names the link.
func TestResolverInternalErrorNeverRedirects(t *testing.T) {
	logger, logs := captureLog()
	svc := &fakeService{resolveFn: func(string) app.Decision {
		return app.Decision{Kind: app.DecisionInternalError, LinkName: "reports", Link: reportsLink(),
			Destination: "https://evil.example/should-not-be-used",
			Err:         errors.New("route 7 of reports: no value for variable \"month\"")}
	}}
	rec := get(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler(), "/reports/S3cr3t-Value")

	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Location") != "" {
		t.Errorf("status = %d, Location = %q; want 500 and none", rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), "evil.example") {
		t.Errorf("the page carries the destination: %q", rec.Body.String())
	}
	log := logs.String()
	if strings.Count(log, `"level":"ERROR"`) != 2 { // the cause and the request record
		t.Errorf("want an error record besides the request record:\n%s", log)
	}
	if got := requestRecord(t, log); got["decision"] != "internal_error" || got["link"] != "reports" || got["status"] != 500.0 {
		t.Errorf("request record = %v", got)
	}
	// The cause the use case gave is what makes the record useful.
	if !strings.Contains(log, `no value for variable \"month\"`) {
		t.Errorf("the error record does not carry the decision's cause:\n%s", log)
	}
	for _, secret := range []string{"S3cr3t", "evil.example"} {
		if strings.Contains(log, secret) {
			t.Errorf("the log contains %q:\n%s", secret, log)
		}
	}
}

// Text that owners and the link model supply stays on its own line: a
// description with a line break in it cannot pass for a line of the page.
func TestResolverPagesKeepTextOnOneLine(t *testing.T) {
	unknown := app.Decision{
		Kind: app.DecisionMissUnknown, LinkName: "reprots", RequestedName: "reprots",
		Suggestions: []app.Suggestion{{
			Name: "reports", Path: "reports/a%0Ab", Edits: 1,
			Description: "Daily\r\nDid you mean go/evil?\u2028x\x00y\tz",
		}},
	}
	known := missKnownDecision()
	known.Link.Description = "First\nSecond"
	known.Reasons[0].Message = "one\ntwo"
	known.Reasons[0].Pattern = "reports/\n{x}"

	for name, tt := range map[string]struct {
		decision  app.Decision
		wantLines int
		wantLine  string
	}{
		"unknown": {unknown, 3, "Did you mean go/reports/a%0Ab? (Daily  Did you mean go/evil? x y z)"},
		"known":   {known, 9, "First Second"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeService{resolveFn: func(string) app.Decision { return tt.decision }}
			body := get(newTestServer(svc, nil).Handler(), "/x").Body.String()

			lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
			if len(lines) != tt.wantLines {
				t.Errorf("the page has %d lines, want %d:\n%q", len(lines), tt.wantLines, body)
			}
			found := false
			for _, line := range lines {
				found = found || line == tt.wantLine
			}
			if !found {
				t.Errorf("no line %q in:\n%q", tt.wantLine, body)
			}
			if strings.ContainsAny(body, "\r\x00\t\u2028") {
				t.Errorf("control characters reached the page: %q", body)
			}
		})
	}
}

func TestOneLine(t *testing.T) {
	tests := map[string]string{
		"":                       "",
		"plain text":             "plain text",
		"żółć 😀":                 "żółć 😀",
		"a\nb":                   "a b",
		"a\r\nb":                 "a  b",
		"a\tb\x00c\x1bd\x7f":     "a b c d ",
		"a\u0085b\u2028c\u2029d": "a b c d",
		"<b>&\"'":                "<b>&\"'", // plain text: nothing to escape
	}
	for in, want := range tests {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// NFR-17 with the L2 decision: the values of variables, the raw path, the
// query, suggestion paths and destinations are shown to the user and never
// written to the log, on the resolver and on every API endpoint that sees
// them.
func TestLogNeverContainsValuesOrSuggestions(t *testing.T) {
	const (
		valueSecret = "V4lue-Secret"
		pathSecret  = "P4th-Secret"
		querySecret = "Qu3ry-Secret"
		destSecret  = "d3st-secret.example"
		descSecret  = "D3scription-Secret"
		bodySecret  = "B0dy-Secret"
	)
	link := reportsLink()
	link.Description = descSecret
	hit := app.Decision{
		Kind: app.DecisionHit, LinkName: "reports", Link: link, Route: &link.Routes[0],
		Values:      map[string]string{"month": valueSecret, "day": "13"},
		Destination: "https://" + destSecret + "/daily?month=" + valueSecret,
	}
	missKnown := app.Decision{
		Kind: app.DecisionMissKnown, LinkName: "reports", Link: link,
		Reasons: []linkmodel.Reason{{RouteID: 9, Pattern: monthPattern, Code: "value_rejected", Variable: "month",
			Value: valueSecret, Accepts: "text", Message: "month doesn't accept \"" + valueSecret + "\"."}},
	}
	missUnknown := app.Decision{
		Kind: app.DecisionMissUnknown, LinkName: "reprots", RequestedName: "reprots",
		Suggestions: []app.Suggestion{{Name: "reports", Path: "reports/" + valueSecret + "/13", Edits: 1, Resolves: true,
			Description: descSecret, Destination: "https://" + destSecret + "/" + valueSecret}},
	}
	internal := app.Decision{Kind: app.DecisionInternalError, LinkName: "reports", Link: link}

	goPath := "/reports/" + pathSecret + "/13?token=" + querySecret
	apiPath := "/-/api/v1/resolve?path=reports%2F" + pathSecret + "%2F13&token=" + querySecret
	jsonBody := `{"description":"` + bodySecret + `","linkId":null,"routes":[{"pattern":"reports/{month}","destination":"https://` +
		destSecret + `/{month}","samples":{"month":["` + valueSecret + `"]}}]}`
	patchBody := `{"description":"` + bodySecret + `","routes":[{"pattern":"reports/{month}","destination":"https://` +
		destSecret + `/{month}","variables":{"month":{"example":"` + valueSecret + `"}}}],"version":1}`

	tests := []struct {
		name     string
		req      request
		decision app.Decision
		want     map[string]any
		shown    string // must be in the response, to prove the test saw the data
	}{
		{"resolver hit", request{target: goPath}, hit,
			map[string]any{"status": 302.0, "decision": "hit", "link": "reports"}, valueSecret},
		{"resolver known miss", request{target: goPath}, missKnown,
			map[string]any{"status": 404.0, "decision": "miss_known", "link": "reports"}, valueSecret},
		{"resolver unknown miss", request{target: goPath}, missUnknown,
			map[string]any{"status": 404.0, "decision": "miss_unknown", "link": "reprots"}, valueSecret},
		{"resolver internal error", request{target: goPath}, internal,
			map[string]any{"status": 500.0, "decision": "internal_error", "link": "reports"}, ""},
		{"API hit", request{target: apiPath}, hit,
			map[string]any{"status": 200.0, "area": "internal", "route": "/-/api/v1/resolve"}, valueSecret},
		{"API known miss", request{target: apiPath}, missKnown,
			map[string]any{"status": 200.0, "route": "/-/api/v1/resolve"}, valueSecret},
		{"API unknown miss", request{target: apiPath}, missUnknown,
			map[string]any{"status": 200.0, "route": "/-/api/v1/resolve"}, valueSecret},
		{"API internal error", request{target: apiPath}, internal,
			map[string]any{"status": 500.0, "route": "/-/api/v1/resolve"}, ""},
		{"API malformed query", request{target: "/-/api/v1/resolve?path=" + pathSecret + "&path=" + querySecret}, hit,
			map[string]any{"status": 400.0, "route": "/-/api/v1/resolve"}, ""},
		{"validate", request{method: http.MethodPost, target: "/-/api/v1/links/validate?x=" + querySecret,
			headers: map[string]string{"Content-Type": "application/json"}, body: jsonBody}, hit,
			map[string]any{"status": 200.0, "route": "/-/api/v1/links/validate"}, valueSecret},
		{"update", request{method: http.MethodPatch, target: "/-/api/v1/links/12?x=" + querySecret,
			headers: map[string]string{"Content-Type": "application/json"}, body: patchBody}, hit,
			map[string]any{"status": 200.0, "route": "/-/api/v1/links/{id}"}, ""},
		{"get", request{target: "/-/api/v1/links/12?x=" + querySecret}, hit,
			map[string]any{"status": 200.0, "route": "/-/api/v1/links/{id}"}, ""},
		{"delete", request{method: http.MethodDelete, target: "/-/api/v1/links/12?version=1&x=" + querySecret}, hit,
			map[string]any{"status": 204.0, "route": "/-/api/v1/links/{id}"}, ""},
		{"an address that is not a link ID", request{target: "/-/api/v1/links/" + pathSecret}, hit,
			map[string]any{"status": 404.0, "route": "/-/api/v1/links/{id}"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, logs := captureLog()
			svc := &fakeService{
				resolveFn: func(string) app.Decision { return tt.decision },
				validateFn: func(app.ValidateRequest) app.ValidateResult {
					return app.ValidateResult{Routes: []app.RouteCheck{{Samples: map[string]map[string]bool{"month": {valueSecret: true}}}}}
				},
			}
			rec := tt.req.send(newTestServer(svc, func(o *Options) { o.Logger = logger }).Handler())

			shown := rec.Body.String() + rec.Header().Get("Location")
			if tt.shown != "" && !strings.Contains(shown, tt.shown) {
				t.Fatalf("the response does not show %q, so the test proves nothing: %s", tt.shown, shown)
			}
			log := logs.String()
			for _, secret := range []string{valueSecret, pathSecret, querySecret, destSecret, descSecret, bodySecret,
				strings.ToLower(valueSecret), strings.ToLower(pathSecret), "token", "path=", "?", "%2F", "month"} {
				if strings.Contains(log, secret) {
					t.Errorf("the log contains %q:\n%s", secret, log)
				}
			}
			record := requestRecord(t, log)
			for key, want := range tt.want {
				if record[key] != want {
					t.Errorf("%s = %v, want %v\n%s", key, record[key], want, log)
				}
			}
		})
	}
}

// missUnknownWithUsage is a miss on "ghh/a/b" next to two links: gh, which
// does not take "a/b" and has a route for its bare name, and gha, which does.
func missUnknownWithUsage() app.Decision {
	gh := &linkmodel.Link{ID: 2, Name: "gh", Description: "GitHub", Routes: []linkmodel.Route{
		{ID: 1, Pattern: "gh", Destination: "https://github.com/acme"},
		{ID: 2, Position: 1, Pattern: "gh/{repo}", Destination: "https://github.com/acme/{repo}"},
		{ID: 3, Position: 2, Pattern: "gh/{repo}/pr/{num:int}", Destination: "https://github.com/acme/{repo}/pull/{num}"},
	}}
	gha := &linkmodel.Link{ID: 3, Name: "gha", Routes: []linkmodel.Route{
		{ID: 4, Pattern: "gha/{a}/{b}", Destination: "https://example.com/actions/{a}/{b}"},
	}}
	return app.Decision{
		Kind: app.DecisionMissUnknown, LinkName: "ghh", RequestedName: "ghh",
		Suggestions: []app.Suggestion{
			{Name: "gh", Path: "gh", Edits: 1, Description: "GitHub", Link: gh},
			{Name: "gha", Path: "gha/a/b", Edits: 1, Resolves: true, Destination: "https://example.com/actions/a/b", Link: gha},
		},
	}
}
