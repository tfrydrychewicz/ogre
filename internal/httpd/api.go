package httpd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/tfrydrychewicz/ogre/internal/app"
	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

const (
	apiPrefix = internalPrefix + "api/v1/"
	// healthzPath answers "ok" while the server runs.
	healthzPath = internalPrefix + "healthz"

	// product identifies an Ogre server to another one: Listen asks the
	// meta endpoint who holds a busy port. Plan section 3.1 lists it among
	// the places the product name lives.
	product    = "ogre"
	apiVersion = 1

	// maxBodyBytes bounds a request body. A link with every route at its
	// longest is far below it.
	maxBodyBytes = 1 << 20
)

// internalMux routes everything under "/-/". ServeMux is safe here, unlike
// at the root: serveInternal has already refused paths it would rewrite.
func (s *Server) internalMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(healthzPath, byMethod{http.MethodGet: s.handleHealthz})
	mux.Handle(apiPrefix+"meta", byMethod{http.MethodGet: s.handleMeta})
	mux.Handle(apiPrefix+"links", byMethod{http.MethodGet: s.handleListLinks, http.MethodPost: s.handleCreateLink})
	// The more specific pattern wins over "{id}", so "validate" is never
	// read as a link ID.
	mux.Handle(apiPrefix+"links/validate", byMethod{http.MethodPost: s.handleValidate})
	mux.Handle(apiPrefix+"links/{id}", byMethod{
		http.MethodGet:    s.handleGetLink,
		http.MethodPatch:  s.handleUpdateLink,
		http.MethodDelete: s.handleDeleteLink,
	})
	mux.Handle(apiPrefix+"resolve", byMethod{http.MethodGet: s.handleResolve})
	// Unknown API paths answer in the API's error shape, not with a UI page.
	mux.HandleFunc(internalPrefix+"api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, codeNotFound, "There is no such API endpoint.", nil)
	})
	s.addPageRoutes(mux)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeText(w, r, http.StatusOK, "ok")
}

// metaJSON is how a client, the extension, or another Ogre server recognises
// this server and learns which API it speaks.
type metaJSON struct {
	Product string `json:"product"`
	Version string `json:"version"`
	API     int    `json:"api"`
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, metaJSON{Product: product, Version: s.opts.Version, API: apiVersion})
}

// linkRequest is the body of POST /links. The name is never sent: it is the
// first segment all routes share.
type linkRequest struct {
	Description string         `json:"description"`
	Routes      []routeRequest `json:"routes"`
}

// routeRequest is one route of a request body. The array order is the route
// order.
type routeRequest struct {
	// ID names the saved route this entry replaces, so that an edit keeps
	// the route's identity. Absent, null or 0 means a new route.
	ID          int64  `json:"id"`
	Pattern     string `json:"pattern"`
	Destination string `json:"destination"`
	// Variables optionally describes the pattern's variables, by name.
	Variables map[string]variableRequest `json:"variables"`
}

type variableRequest struct {
	Description string `json:"description"`
	Example     string `json:"example"`
}

// draftRoute converts a route of a request without judging it: whether an ID
// belongs to the link, or a described variable exists, is for the Service to
// say.
func (rt routeRequest) draftRoute() app.DraftRoute {
	out := app.DraftRoute{ID: rt.ID, Pattern: rt.Pattern, Destination: rt.Destination}
	if len(rt.Variables) > 0 {
		// A JSON object has no order; sorting makes the same request yield
		// the same draft, and with it the same problems in the same order.
		names := make([]string, 0, len(rt.Variables))
		for name := range rt.Variables {
			names = append(names, name)
		}
		sort.Strings(names)
		out.Variables = make([]linkmodel.VariableMeta, 0, len(names))
		for _, name := range names {
			v := rt.Variables[name]
			out.Variables = append(out.Variables, linkmodel.VariableMeta{Name: name, Description: v.Description, Example: v.Example})
		}
	}
	return out
}

func newDraft(description string, routes []routeRequest) app.Draft {
	draft := app.Draft{Description: description, Routes: make([]app.DraftRoute, 0, len(routes))}
	for _, rt := range routes {
		draft.Routes = append(draft.Routes, rt.draftRoute())
	}
	return draft
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if rejected := decodeBody(w, r, &req); rejected != nil {
		writeError(w, r, rejected.status, rejected.code, rejected.message, nil)
		return
	}
	draft := newDraft(req.Description, req.Routes)

	link, err := s.svc.CreateLink(r.Context(), s.opts.Actor, draft)
	if err != nil {
		s.serviceError(w, r, "create link", draft, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, s.linkJSON(r, link))
}

// decodeBody reads one JSON value into dst. It refuses unknown fields and
// anything after the value, so a typo in a field name is reported instead of
// silently saving a link without it.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) *rejection {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeRejection(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return &rejection{http.StatusBadRequest, codeMalformed, "The request body has extra data after the JSON value."}
		}
		return decodeRejection(err)
	}
	return nil
}

func decodeRejection(err error) *rejection {
	var tooLarge *http.MaxBytesError
	var wrongType *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		return &rejection{http.StatusRequestEntityTooLarge, codeTooLarge, "The request body is larger than 1 MiB."}
	case errors.As(err, &wrongType):
		return &rejection{http.StatusBadRequest, codeMalformed, "The field \"" + wrongType.Field + "\" has the wrong type."}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no error type for this; the field name it quotes
		// is useful to whoever wrote the request.
		return &rejection{http.StatusBadRequest, codeMalformed,
			"The request body has an " + strings.TrimPrefix(err.Error(), "json: ") + "."}
	default:
		return &rejection{http.StatusBadRequest, codeMalformed, "The request body is not valid JSON."}
	}
}

// problemJSON is a linkmodel.Problem on the wire.
type problemJSON struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	// Start and End mark the problem in the text of its field as offsets in
	// UTF-16 code units: the index a browser's JavaScript uses for the same
	// string, so a form can highlight the span without converting.
	Start int `json:"start"`
	End   int `json:"end"`
}

// routeProblemsJSON holds the problems of one route of a draft.
type routeProblemsJSON struct {
	Problems []problemJSON `json:"problems"`
}

// writeInvalid answers 422 for a draft that failed validation. It lists
// warnings beside the errors, as the Service reported them.
//
// The problems are grouped the way the validate endpoint groups them (plan
// section 3.5): details.routes has one entry per route of the request, in the
// same order, each with that route's problems; details.problems has those
// that belong to no single route, such as a request without routes.
func writeInvalid(w http.ResponseWriter, r *http.Request, draft app.Draft, invalid *app.ValidationError) {
	routes := make([]routeProblemsJSON, len(invalid.Routes))
	for i, problems := range invalid.Routes {
		var route app.DraftRoute
		if i < len(draft.Routes) {
			route = draft.Routes[i]
		}
		routes[i] = routeProblemsJSON{Problems: problemsJSON(problems, route)}
	}
	writeError(w, r, http.StatusUnprocessableEntity, codeLinkInvalid,
		"This link can't be saved until its problems are fixed.", map[string]any{
			"problems": problemsJSON(invalid.Problems, app.DraftRoute{}),
			"routes":   routes,
		})
}

// problemsJSON converts problems for the wire. route is the text they point
// into; its pattern and destination are needed to convert the offsets.
func problemsJSON(problems []linkmodel.Problem, route app.DraftRoute) []problemJSON {
	out := make([]problemJSON, 0, len(problems))
	for _, p := range problems {
		text := route.Pattern
		if p.Field == linkmodel.FieldDestination {
			text = route.Destination
		}
		out = append(out, problemJSON{
			Severity: p.Severity, Field: p.Field, Code: p.Code, Message: p.Message,
			Start: utf16Offset(text, p.Start), End: utf16Offset(text, p.End),
		})
	}
	return out
}

// utf16Offset converts a byte offset into text, which is how the link model
// counts, into the offset of the same position in UTF-16 code units, which is
// how the browser that draws the highlight counts. The two differ as soon as
// a non-ASCII character comes before the position.
//
// An offset past the end of text counts as the end. Text decoded from a JSON
// request is always valid UTF-8.
func utf16Offset(text string, byteOffset int) int {
	byteOffset = max(0, min(byteOffset, len(text)))
	units := 0
	for _, r := range text[:byteOffset] {
		units += utf16.RuneLen(r)
	}
	return units
}

// serviceError turns an error of the use cases into a response. Only errors
// the caller can act on are described; anything else is logged and answered
// with a bare 500, so internals never leak into a response.
//
// draft is what the request asked to save, needed to convert the offsets of
// validation problems; requests that save nothing pass the zero Draft.
func (s *Server) serviceError(w http.ResponseWriter, r *http.Request, action string, draft app.Draft, err error) {
	var (
		invalid   *app.ValidationError
		taken     *app.NameTakenError
		conflict  *app.VersionConflictError
		forbidden *app.ForbiddenError
	)
	switch {
	case errors.As(err, &invalid):
		writeInvalid(w, r, draft, invalid)
	case errors.Is(err, app.ErrNameTaken):
		// DIR-4: say which link has the name and whether the caller could
		// edit that one instead.
		var details map[string]any
		if errors.As(err, &taken) {
			details = map[string]any{
				"existing": linkRefJSON{ID: taken.Existing.ID, Name: taken.Existing.Name},
				"canEdit":  taken.Editable,
			}
		}
		writeError(w, r, http.StatusConflict, codeNameTaken, "A link with this name already exists.", details)
	case errors.Is(err, app.ErrVersionConflict):
		var details map[string]any
		if errors.As(err, &conflict) {
			details = map[string]any{"currentVersion": conflict.CurrentVersion}
		}
		writeError(w, r, http.StatusConflict, codeVersionConflict,
			"This link was changed since you opened it. Load it again before saving.", details)
	case errors.Is(err, app.ErrNotFound):
		writeLinkNotFound(w, r)
	case errors.Is(err, app.ErrForbidden):
		// PRD 5.7, save example 5: the refusal names the owner and points at
		// the link, so the user knows whom to ask and what about.
		message, details := "You are not allowed to do this.", map[string]any(nil)
		if errors.As(err, &forbidden) {
			name := forbidden.Link.Name
			message = "You can't change go/" + name + ". Only its owner or an administrator can."
			if forbidden.Owner != "" {
				message = "You can't change go/" + name + ": it belongs to " + forbidden.Owner +
					". Ask them to change it, or to hand it over."
			}
			details = map[string]any{
				"link":  linkRefJSON{ID: forbidden.Link.ID, Name: name},
				"owner": ownerJSON{ID: forbidden.Link.OwnerID, DisplayName: forbidden.Owner},
			}
		}
		writeError(w, r, http.StatusForbidden, codeForbidden, message, details)
	default:
		s.log.Error(action+" failed", "error", err)
		writeError(w, r, http.StatusInternalServerError, codeInternal, "Something went wrong on the server.", nil)
	}
}

func writeLinkNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, codeLinkNotFound, "There is no such link.", nil)
}

// linkRefJSON points at a link without describing it.
type linkRefJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// linkJSON is a link as every endpoint returns it (plan section 3.5).
type linkJSON struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Owner       ownerJSON   `json:"owner"`
	Version     int         `json:"version"`
	CanEdit     bool        `json:"canEdit"`
	Routes      []routeJSON `json:"routes"`
	Usage       usageJSON   `json:"usage"`
	CreatedAt   string      `json:"createdAt"`
	UpdatedAt   string      `json:"updatedAt"`
}

type ownerJSON struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
}

type routeJSON struct {
	ID          int64  `json:"id"`
	Pattern     string `json:"pattern"`
	Destination string `json:"destination"`
	// Variables lists the pattern's variables in pattern order; empty for a
	// route of literals only.
	Variables []variableJSON `json:"variables"`
}

// variableJSON is one variable of a saved route. Type and Accepts are read
// from the stored pattern by the link model; Description and Example are
// what the owner wrote beside it.
type variableJSON struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Accepts     string `json:"accepts"`
	Description string `json:"description"`
	Example     string `json:"example"`
}

// usageJSON is all zeros until usage counting arrives in L5.
type usageJSON struct {
	Total      int64   `json:"total"`
	Last30Days int64   `json:"last30Days"`
	LastUsedAt *string `json:"lastUsedAt"`
}

func (s *Server) linkJSON(r *http.Request, l linkmodel.Link) linkJSON {
	out := linkJSON{
		ID:          l.ID,
		Name:        l.Name,
		Description: l.Description,
		Owner:       ownerJSON{ID: l.OwnerID, DisplayName: s.svc.Owner(r.Context(), l)},
		Version:     l.Version,
		// The rule is the Service's; the same one guards the edit itself.
		CanEdit:   s.svc.CanEdit(s.opts.Actor, l),
		Routes:    make([]routeJSON, 0, len(l.Routes)),
		CreatedAt: formatTime(l.CreatedAt),
		UpdatedAt: formatTime(l.UpdatedAt),
	}
	for _, rt := range l.Routes {
		// The link model reads the pattern; nothing here knows its syntax.
		infos := linkmodel.RouteVariables(rt)
		variables := make([]variableJSON, 0, len(infos))
		for _, v := range infos {
			variables = append(variables, variableJSON{
				Name: v.Name, Type: v.Type, Accepts: v.Accepts, Description: v.Description, Example: v.Example,
			})
		}
		out.Routes = append(out.Routes, routeJSON{ID: rt.ID, Pattern: rt.Pattern, Destination: rt.Destination, Variables: variables})
	}
	return out
}

// formatTime renders a time as the API does everywhere: UTC, RFC 3339.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
