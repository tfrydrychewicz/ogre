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
}

type validateRouteRequest struct {
	routeRequest
	// Samples are values to try on the route's variables, by variable name.
	Samples map[string][]string `json:"samples"`
}

// validateJSON is the answer of the validate endpoint (plan section 3.5).
type validateJSON struct {
	Valid  bool             `json:"valid"`
	Name   nameCheckJSON    `json:"name"`
	Routes []routeCheckJSON `json:"routes"`
	// Problems are those that belong to no single route.
	Problems []problemJSON `json:"problems"`
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
	}
	writeJSON(w, r, http.StatusOK, out)
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
