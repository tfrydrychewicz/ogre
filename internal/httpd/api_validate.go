package httpd

import (
	"net/http"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

// validateRequest is the body of POST /links/validate: a draft as it would be
// saved, plus what only a check needs.
type validateRequest struct {
	// LinkID is the link being edited; null, absent or 0 for a new link.
	LinkID      int64                  `json:"linkId"`
	Description string                 `json:"description"`
	Routes      []validateRouteRequest `json:"routes"`

	// What the create and edit form asks for beyond the check itself
	// (plan L3.6). Both are optional, so the answer to a request without
	// them is exactly the one of plan section 3.5.

	// DescribeVariables asks for each route's variables as the form's
	// variables table shows them ("variables" in each route's answer).
	DescribeVariables bool `json:"describeVariables"`
	// Preview asks where the draft would send one request if it were saved
	// as it is ("preview" in the answer): the form's live preview.
	Preview *previewRequestJSON `json:"preview"`
	// ExplainSamples asks, beside each route's "samples", for the sentence a
	// refused sample would get ("sampleReasons"): what the launcher shows
	// beside a field as a value is typed (VAR-5).
	ExplainSamples bool `json:"explainSamples"`
}

// previewRequestJSON is the "preview" of a validate request.
type previewRequestJSON struct {
	// Route is the index of the route the path was built for; absent or
	// null for none in particular.
	Route *int `json:"route"`
	// Path is the go-link path as it would follow "go/", each value
	// percent-encoded (encodeURIComponent). It is not encoded a second time:
	// it travels in a JSON body, not in a query string.
	Path string `json:"path"`
}

type validateRouteRequest struct {
	routeRequest
	// Samples are values to try on the route's variables, by variable name.
	Samples map[string][]string `json:"samples"`
	// Expressions are regular expressions to check on their own, by
	// variable name: what the form's variables table holds for a regex
	// variable (plan L3.6). Optional; the answer's "expressions" says what
	// each one lacks, in the terms of the expression alone.
	Expressions map[string]string `json:"expressions"`
}

// validateJSON is the answer of the validate endpoint (plan section 3.5).
type validateJSON struct {
	Valid  bool             `json:"valid"`
	Name   nameCheckJSON    `json:"name"`
	Routes []routeCheckJSON `json:"routes"`
	// Problems are those that belong to no single route.
	Problems []problemJSON `json:"problems"`
	// Preview is present only when the request asked for one.
	Preview *previewJSON `json:"preview,omitempty"`
}

// nameCheckJSON mirrors the details of a 409 "link.name_taken": the link that
// holds the name, and whether the caller could edit that one instead.
type nameCheckJSON struct {
	Value     string       `json:"value"`
	Available bool         `json:"available"`
	Existing  *linkRefJSON `json:"existing"`
	// CanEdit is about Existing; it is false when Existing is null.
	CanEdit bool `json:"canEdit"`
}

type routeCheckJSON struct {
	Tokens   []tokenJSON                `json:"tokens"`
	Problems []problemJSON              `json:"problems"`
	Samples  map[string]map[string]bool `json:"samples"`
	// SampleReasons says why each refused sample is refused, by variable and
	// value; the sentence a miss page would show. Present only when the
	// request set explainSamples and some sample was refused.
	SampleReasons map[string]map[string]string `json:"sampleReasons,omitempty"`
	// Variables is present only when the request set describeVariables.
	Variables *[]variableCheckJSON `json:"variables,omitempty"`
	// Expressions answers the route's "expressions", by variable name;
	// present only when the request sent some.
	Expressions map[string]expressionCheckJSON `json:"expressions,omitempty"`
}

// expressionCheckJSON is the verdict on one expression of a route's
// "expressions" (app.ExpressionProblem): Code and Message are empty when it
// is valid.
type expressionCheckJSON struct {
	Valid   bool   `json:"valid"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// variableCheckJSON is one variable of a pattern being written
// (app.VariableCheck). Start and End count UTF-16 code units and include the
// braces: the span the form rewrites when the type is changed in the table.
type variableCheckJSON struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Accepts    string `json:"accepts"`
	Expression string `json:"expression"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
}

// previewJSON is what a draft would do with one request (app.Preview). Each
// kind carries its own fields: destination, route and values for "hit",
// reasons for "miss_known", message for "bad_request" and "invalid".
type previewJSON struct {
	Kind        string              `json:"kind"`
	Route       *int                `json:"route,omitempty"`
	Destination string              `json:"destination,omitempty"`
	Values      map[string]string   `json:"values,omitempty"`
	Reasons     []previewReasonJSON `json:"reasons,omitempty"`
	Message     string              `json:"message,omitempty"`
}

// previewReasonJSON is the reason of a resolve miss (reasonJSON), with the
// index of the draft route it is about.
type previewReasonJSON struct {
	Route int `json:"route"`
	reasonJSON
}

// tokenJSON is one segment of a pattern. Start and End count UTF-16 code
// units, like every offset of the API. Name and Type are present for a
// variable only.
type tokenJSON struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Name  string `json:"name,omitempty"`
	Type  string `json:"type,omitempty"`
}

// handleValidate answers 200 whatever the draft is like: the check itself
// succeeded, and what it found is the body. Nothing is written.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var body validateRequest
	if rejected := decodeBody(w, r, &body); rejected != nil {
		writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
		return
	}
	req := app.ValidateRequest{
		LinkID: body.LinkID,
		Draft:  app.Draft{Description: body.Description, Routes: make([]app.DraftRoute, 0, len(body.Routes))},
	}
	anySamples := false
	for _, rt := range body.Routes {
		req.Draft.Routes = append(req.Draft.Routes, rt.draftRoute())
		anySamples = anySamples || len(rt.Samples) > 0
	}
	if anySamples {
		req.Samples = make([]map[string][]string, len(body.Routes))
		for i, rt := range body.Routes {
			req.Samples[i] = rt.Samples
		}
	}

	result := s.svc.Validate(r.Context(), s.opts.Actor, req)

	out := validateJSON{
		Valid: result.Valid,
		Name: nameCheckJSON{
			Value:     result.Name.Value,
			Available: result.Name.Available,
		},
		Routes:   make([]routeCheckJSON, len(result.Routes)),
		Problems: problemsJSON(result.Problems, app.DraftRoute{}),
	}
	if existing := result.Name.Existing; existing != nil {
		out.Name.Existing = &linkRefJSON{ID: existing.ID, Name: existing.Name}
		out.Name.CanEdit = result.Name.ExistingEditable
	}
	for i, check := range result.Routes {
		// The route's own text, to convert the offsets that point into it.
		var route app.DraftRoute
		if i < len(req.Draft.Routes) {
			route = req.Draft.Routes[i]
		}
		samples := check.Samples
		if samples == nil {
			samples = map[string]map[string]bool{}
		}
		out.Routes[i] = routeCheckJSON{
			Tokens:   tokensJSON(check.Tokens, route.Pattern),
			Problems: problemsJSON(check.Problems, route),
			Samples:  samples,
		}
		if body.ExplainSamples && len(check.SampleReasons) > 0 {
			out.Routes[i].SampleReasons = check.SampleReasons
		}
		if i < len(body.Routes) && len(body.Routes[i].Expressions) > 0 {
			out.Routes[i].Expressions = make(map[string]expressionCheckJSON, len(body.Routes[i].Expressions))
			for name, expression := range body.Routes[i].Expressions {
				code, message := app.ExpressionProblem(expression)
				out.Routes[i].Expressions[name] = expressionCheckJSON{Valid: code == "", Code: code, Message: message}
			}
		}
		if body.DescribeVariables {
			variables := variablesJSON(app.DraftVariables(route.Pattern), route.Pattern)
			out.Routes[i].Variables = &variables
		}
	}
	if body.Preview != nil {
		out.Preview = previewToJSON(app.PreviewDraft(req.Draft, result, previewRequest(body.Preview)))
	}
	writeJSON(w, r, http.StatusOK, out)
}

func previewRequest(p *previewRequestJSON) app.PreviewRequest {
	route := -1
	if p.Route != nil && *p.Route >= 0 {
		route = *p.Route
	}
	return app.PreviewRequest{Route: route, Path: p.Path}
}

func previewToJSON(p app.Preview) *previewJSON {
	out := &previewJSON{Kind: p.Kind, Message: p.Message}
	if p.Kind == app.PreviewHit {
		route := p.Route
		out.Route, out.Destination, out.Values = &route, p.Destination, p.Values
		if out.Values == nil {
			out.Values = map[string]string{}
		}
	}
	if p.Kind == app.PreviewMissKnown {
		out.Reasons = make([]previewReasonJSON, 0, len(p.Reasons))
		for _, reason := range p.Reasons {
			out.Reasons = append(out.Reasons, previewReasonJSON{Route: reason.Route, reasonJSON: newReasonJSON(reason.Reason)})
		}
	}
	return out
}

func variablesJSON(variables []app.VariableCheck, pattern string) []variableCheckJSON {
	out := make([]variableCheckJSON, 0, len(variables))
	for _, v := range variables {
		out = append(out, variableCheckJSON{
			Name: v.Name, Type: v.Type, Accepts: v.Accepts, Expression: v.Expression,
			Start: utf16Offset(pattern, v.Start), End: utf16Offset(pattern, v.End),
		})
	}
	return out
}

func tokensJSON(tokens []linkmodel.Token, pattern string) []tokenJSON {
	out := make([]tokenJSON, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, tokenJSON{
			Kind:  t.Kind,
			Start: utf16Offset(pattern, t.Start),
			End:   utf16Offset(pattern, t.End),
			Name:  t.Name,
			Type:  t.Type,
		})
	}
	return out
}
