package httpd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

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
